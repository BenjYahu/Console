package serverdata

type Account struct {
	DisplayName string
	Username string
	Commands []string
	TokenEnv string
}

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

var EnabledCommands = []string{
	"on",        // .feature on  <player> <feature>
	"off",       // .feature off <player> <feature>
	"notify",    // .notification send <player> <message>
	"broadcast", // .broadcast send <message>
}
