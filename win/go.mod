module github.com/tiancaiamao/ai/win

go 1.24.0

require (
	github.com/sminez/ad/win v0.0.0-20260211034838-14a2a7a40d33
	github.com/tiancaiamao/ai v0.0.0
)

require (
	9fans.net/go v0.0.7 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
)

replace (
	github.com/sminez/ad/win => github.com/tiancaiamao/ad/win v0.0.0-20260211034838-14a2a7a40d33
	github.com/tiancaiamao/ai => ../
)
