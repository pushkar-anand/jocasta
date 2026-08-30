package main

type CLI struct {
	ConfigFile string `name:"config" short:"c" help:"Path to configuration file." default:"jocasta.yaml"`
	LogLevel   string `name:"log-level" help:"Override log level (debug, info, warn, error)."`
	LogFormat  string `name:"log-format" help:"Override log format (text, json)."`

	Serve ServeCmd `cmd:"" help:"Start the Jocasta web server." default:"withargs"`
	Scan  ScanCmd  `cmd:"" help:"Scan network addresses with ICMP echo requests."`
}
