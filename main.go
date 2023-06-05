package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"flag"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	ffmpeg "github.com/u2takey/ffmpeg-go"

	"log"
	"os"
	"os/signal"

	// Packages
	//whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/skrashevich/whisper.cpp-telegram/pkg/model-downloader"
)

var (
	// The models which will be downloaded, if no model is specified as an argument
	modelNames = []string{"ggml-tiny.en", "ggml-tiny", "ggml-base.en", "ggml-base", "ggml-small.en", "ggml-small", "ggml-medium.en", "ggml-medium", "ggml-large-v1", "ggml-large"}
)

func main() {
	token := flag.String("token", "", "Telegram bot token")
	model := flag.String("model", "ggml-medium", "Path to the model file")
	language := flag.String("language", "auto", "Spoken language")
	no_context := flag.Bool("no_context", true, "do not use past transcription (if any) as initial prompt for the decoder")
	translate := flag.Bool("translate", false, "Translate from source language to english")
	offset := flag.Duration("offset", 0, "Time offset")
	duration := flag.Duration("duration", 0, "Duration of audio to process")
	threads := flag.Uint("threads", 0, "Number of threads to use")
	speedup := flag.Bool("speedup", false, "Enable speedup")
	max_len := flag.Uint("max-len", 0, "Maximum segment length in characters")
	max_tokens := flag.Uint("max-tokens", 0, "Maximum tokens per segment")
	word_thold := flag.Float64("word-thold", 0, "Maximum segment score")
	tokens := flag.Bool("tokens", false, "Display tokens")
	colorize := flag.Bool("colorize", false, "Colorize tokens")
	//flag.String("out", "", "Output format (srt, none or leave as empty string)")

	flag.Parse()

	// Create a channel to receive the signals
	sigChan := make(chan os.Signal, 1)

	// Notify the channel for specific signals
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start a goroutine to handle signals
	go func() {
		// Block until a signal is received
		sig := <-sigChan
		fmt.Printf("Received signal: %s\n", sig)

		// Perform any cleanup or shutdown operations here

		// Terminate the program
		os.Exit(0)
	}()

	if !isInSet(*model, modelNames) {
		fmt.Printf("Model must be one of: %s\n", strings.Join(modelNames, ","))
		os.Exit(1)
	}

	// Get output path
	modelspath, err := modeldownloader.GetOut()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(-1)
	}

	log.Printf("Use model %s", *model)

	// Create context which quits on SIGINT or SIGQUIT
	ctx := modeldownloader.ContextForSignal(os.Interrupt, syscall.SIGQUIT)

	// Progress filehandle
	progress := os.Stdout

	url, err := modeldownloader.URLForModel(*model)
	modelfile := filepath.Join(modelspath, filepath.Base(url))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	} else if modelfile, err := modeldownloader.Download(ctx, progress, url, modelspath); err == nil || err == io.EOF {
		fmt.Fprintln(progress, "Model downloaded")
	} else if err == context.Canceled {
		os.Remove(modelfile)
		fmt.Fprintln(progress, "\nInterrupted")
		os.Exit(1)
	} else if err == context.DeadlineExceeded {
		os.Remove(modelfile)
		fmt.Fprintln(progress, "Timeout downloading model")
		os.Exit(1)
	} else {
		os.Remove(modelfile)
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	bot, err := tgbotapi.NewBotAPI(*token)
	if err != nil {
		log.Printf("Not bot token provided")
		os.Exit(1)
	}

	bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	wp := WPInit()

	// Load model
	err = wp.LoadModel(modelfile, WhisperParams{
		language:   *language,
		no_context: *no_context,
		translate:  *translate,
		offset:     *offset,
		duration:   *duration,
		threads:    *threads,
		speedup:    *speedup,
		max_len:    *max_len,
		max_tokens: *max_tokens,
		word_thold: *word_thold,
		tokens:     *tokens,
		colorize:   *colorize,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log.Printf("Model %s loaded", *model)
	//wp.Prepare()

	//log.Printf("Model %s prepared", *model)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil { // ignore any non-Message Updates
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		// Check if the message is a voice or audio message
		if update.Message.Voice != nil || update.Message.Audio != nil {
			fileID := ""
			if update.Message.Voice != nil {
				fileID = update.Message.Voice.FileID
			} else if update.Message.Audio != nil {
				fileID = update.Message.Audio.FileID
			}

			file, err := bot.GetFileDirectURL(fileID)
			if err != nil {
				log.Println(err)
				continue
			}
			start := time.Now()
			output, err := wp.Transcribe(file)
			if err != nil {
				log.Println(err)
				output = err.Error()
			}
			recorgise_duration := time.Since(start)
			output += fmt.Sprintf("\n\n%.2f seconds", recorgise_duration.Seconds())
			// Send the output of the binary execution as a reply
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, string(output))
			bot.Send(msg)
		}
	}
}

// convertToWav converts an audio file to 16kHz WAV format
func convertToWav(input, output string) error {
	err := ffmpeg.Input(input).
		Output(output, ffmpeg.KwArgs{"ar": "16000", "f": "wav"}).
		OverWriteOutput().
		Run()
	if err != nil {
		log.Fatal(err)
		return err
	}
	return nil
}

// TempFileName generates a temporary filename for use in testing or whatever
func TempFileName(prefix, suffix string) string {
	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	return filepath.Join(os.TempDir(), prefix+hex.EncodeToString(randBytes)+suffix)
}
func isInSet(value string, set []string) bool {
	for _, v := range set {
		if v == value {
			return true
		}
	}
	return false
}
