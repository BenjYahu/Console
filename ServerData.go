// Package serverdata is the console's editable configuration: who may control clients and which
// commands they may send.
//
// This file is committed to a public repository, so it must never hold a token or password. It
// carries the display name, the Minecraft name and the allowed commands only; each account's token
// lives in a Vercel environment variable (see TokenEnv). That split is what stops somebody from
// posting a command while claiming to be another account: knowing the name, or having the source,
// gets you nothing without that account's token.
package serverdata

// Account is one person allowed to send console commands.
type Account struct {
	// DisplayName is what other players see on broadcasts and in the client's console messages.
	DisplayName string

	// Username is the exact Minecraft account this entry belongs to. The client sends it, the API
	// looks it up here and only accepts it together with this account's token.
	Username string

	// Commands limits this account to a subset of EnabledCommands. Leave it empty to allow every
	// enabled command.
	Commands []string

	// TokenEnv is the environment variable holding this account's token. Empty means
	// "CONSOLE_TOKEN_" plus the upper-cased username, e.g. CONSOLE_TOKEN_POOP656.
	TokenEnv string
}

// Accounts is the console whitelist. Delete an entry to cut that person off; add one and set its
// token in the matching environment variable to let somebody new in.
var Accounts = []Account{
	{
		DisplayName: "TLine",
		Username:    "poop656",
		Commands:    []string{"on", "off", "notify", "broadcast"},
	},
	{
		DisplayName: "Lain",
		Username:    "lain",
		Commands:    []string{"on", "off", "notify"},
	},
}

// EnabledCommands lists every console command the API accepts at all. Comment one out to switch it
// off for everybody, whatever their account allows.
var EnabledCommands = []string{
	"on",        // .feature on  <player> <feature>
	"off",       // .feature off <player> <feature>
	"notify",    // .notification send <player> <message>
	"broadcast", // .broadcast send <message>
}
