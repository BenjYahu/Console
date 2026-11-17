package console

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DefaultDatabase is used when MONGO_URI does not name one in its path.
const DefaultDatabase = "revoke"

const (
	eventsCollection   = "console_events"
	countersCollection = "console_counters"
	eventsCounter      = "events"
)

// mongoStore keeps console events in MongoDB, which is what lets serverless instances agree on
// which commands are waiting for which player.
type mongoStore struct {
	events   *mongo.Collection
	counters *mongo.Collection
	indexes  sync.Once
}

func newMongoStore(uri string) (Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(8*time.Second).
		SetConnectTimeout(8*time.Second))

	if err != nil {
		return nil, err
	}

	database := client.Database(databaseName(uri))

	return &mongoStore{
		events:   database.Collection(eventsCollection),
		counters: database.Collection(countersCollection),
	}, nil
}

// databaseName reads the database from the URI path, so mongodb+srv://…/mydb uses mydb.
func databaseName(uri string) string {
	parsed, err := url.Parse(uri)

	if err != nil {
		return DefaultDatabase
	}

	name := strings.Trim(strings.TrimSpace(parsed.Path), "/")

	if name == "" || strings.Contains(name, "/") {
		return DefaultDatabase
	}

	return name
}

func (store *mongoStore) Append(from, action, target, payload string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	store.createIndexes()

	id, err := store.nextID(ctx)

	if err != nil {
		return 0, err
	}

	now := time.Now()

	_, err = store.events.InsertOne(ctx, bson.M{
		"id":        id,
		"from":      from,
		"action":    action,
		"target":    target,
		"targetKey": strings.ToLower(target),
		"payload":   payload,
		"time":      now.UnixMilli(),
		"createdAt": now,
	})

	if err != nil {
		return 0, err
	}

	return id, nil
}

// nextID keeps the cursor monotonic across every instance: the counter document is the single
// place ids come from, so two instances cannot hand out the same one.
func (store *mongoStore) nextID(ctx context.Context) (int64, error) {
	var counter struct {
		Value int64 `bson:"value"`
	}

	err := store.counters.FindOneAndUpdate(ctx,
		bson.M{"_id": eventsCounter},
		bson.M{"$inc": bson.M{"value": int64(1)}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)

	if err != nil {
		return 0, err
	}

	return counter.Value, nil
}

func (store *mongoStore) Since(target string, since int64, cutoff time.Time) ([]Event, int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	store.createIndexes()

	filter := bson.M{
		"id":   bson.M{"$gt": since},
		"time": bson.M{"$gte": cutoff.UnixMilli()},
		"$or": bson.A{
			bson.M{"action": "broadcast"},
			bson.M{"targetKey": strings.ToLower(target)},
		},
	}

	cursor, err := store.events.Find(ctx, filter,
		options.Find().SetSort(bson.M{"id": 1}).SetLimit(MaxEvents))

	if err != nil {
		return nil, 0, err
	}

	defer cursor.Close(ctx)

	events := make([]Event, 0, 8)

	if err := cursor.All(ctx, &events); err != nil {
		return nil, 0, err
	}

	last := since

	for _, event := range events {
		if event.ID > last {
			last = event.ID
		}
	}

	return events, last, nil
}

// createIndexes runs once per instance in the background: queries work without it, it only makes
// them fast and lets MongoDB drop expired commands on its own.
func (store *mongoStore) createIndexes() {
	store.indexes.Do(func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			_, _ = store.events.Indexes().CreateMany(ctx, []mongo.IndexModel{
				{
					Keys:    bson.D{{Key: "id", Value: 1}},
					Options: options.Index().SetUnique(true),
				},
				{
					Keys: bson.D{{Key: "targetKey", Value: 1}, {Key: "id", Value: 1}},
				},
				{
					Keys:    bson.D{{Key: "createdAt", Value: 1}},
					Options: options.Index().SetExpireAfterSeconds(int32(Retention.Seconds())),
				},
			})
		}()
	})
}
