module github.com/skrashevich/whisper.cpp-telegram

go 1.20

require (
	github.com/ggerganov/whisper.cpp/bindings/go v0.0.0-20230528233858-d7c936b44a80
	github.com/go-audio/wav v1.1.0
	github.com/go-telegram-bot-api/telegram-bot-api v4.6.4+incompatible
	github.com/imdario/mergo v0.3.16
	github.com/u2takey/ffmpeg-go v0.4.1
)

require (
	github.com/aws/aws-sdk-go v1.38.20 // indirect
	github.com/go-audio/audio v1.0.0 // indirect
	github.com/go-audio/riff v1.0.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/technoweenie/multipartstreamer v1.0.1 // indirect
	github.com/u2takey/go-utils v0.3.1 // indirect
)

replace github.com/ggerganov/whisper.cpp/bindings/go => ./pkg/whisper
