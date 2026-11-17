package serverdata

type Account struct {
	DisplayName string
	Username string
	Commands []string
	TokenEnv string
}

var Accounts = []Account{
	{
		DisplayName: "tline",
		Username:    "poop656",
		Commands:    []string{"on", "off", "notify", "broadcast"},
	},
	{
		DisplayName: "lain",
		Username:    "poop656",
		Commands:    []string{"on", "off", "notify", "broadcast"},
	},
	{
		DisplayName: "acutebunny",
		Username:    "Adolf_Hit_ler",
		Commands:    []string{"on", "off", "notify", "broadcast"},
	},
}

var EnabledCommands = []string{
	"on",        // .feature on  <player> <feature>
	"off",       // .feature off <player> <feature>
	"notify",    // .notification send <player> <message>
	"broadcast", // .broadcast send <message>
}
