module github.com/arkeep-io/arkeep/agent

go 1.26

require github.com/arkeep-io/arkeep/shared v0.0.0

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
)

replace github.com/arkeep-io/arkeep/shared => ../shared
