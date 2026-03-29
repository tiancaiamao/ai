module github.com/tiancaiamao/ai

go 1.24.0

require (
	github.com/google/uuid v1.6.0
	github.com/sminez/ad/win v0.0.0-20260211034838-14a2a7a40d33
	github.com/stretchr/testify v1.11.1
)

require (
	9fans.net/go v0.0.7 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/genius/orchestrate v0.0.0-00010101000000-000000000000 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/spf13/cobra v1.8.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/sminez/ad/win => github.com/tiancaiamao/ad/win v0.0.0-20260211034838-14a2a7a40d33

replace github.com/genius/orchestrate => ./extension/orchestrate
