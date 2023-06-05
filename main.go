package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/ioutil"
	"net/http"

	"fmt"
	"io"

	"path/filepath"
	"strings"
	"syscall"
	"time"

	"flag"

	ffmpeg "github.com/u2takey/ffmpeg-go"
	"gopkg.in/telebot.v3"

	"gopkg.in/telebot.v3/middleware"

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

type File struct {
	Ok     bool `json:"ok"`
	Result struct {
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
	} `json:"result"`
}

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

	// Get output path
	modelspath, err := modeldownloader.GetOut()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(-1)
	}

	// Create context which quits on SIGINT or SIGQUIT
	ctx := modeldownloader.ContextForSignal(os.Interrupt, syscall.SIGQUIT)

	// Progress filehandle
	progress := os.Stdout

	url, err := modeldownloader.URLForModel(*model)
	modelfile := filepath.Join(modelspath, filepath.Base(url))
	info, err := os.Stat(modelfile)

	if err == nil && info.Size() > 0 {
		log.Printf("Use local model %s", *model)
	} else {
		log.Printf("Download model %s", *model)
		if !isInSet(*model, modelNames) {
			fmt.Printf("Model must be one of: %s\n", strings.Join(modelNames, ","))
			//os.Exit(1)
		}
		if modelfile, err := modeldownloader.Download(ctx, progress, url, modelspath); err == nil || err == io.EOF {
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
	}

	pref := telebot.Settings{
		Token:  *token,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	}
	if err != nil {
		log.Printf("Not bot token provided")
		os.Exit(1)
	}

	bot, err := telebot.NewBot(pref)

	log.Printf("Authorized on account %s", bot.Me.Username)

	wp := WPInit()

	// Load model
	err = wp.LoadModel(modelfile)
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

	bot.Use(middleware.Logger())

	processHandler := func(c telebot.Context, fileURL string) error {
		err := wp.PrepareModel(WhisperParams{
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
			return err
		}

		start := time.Now()
		output, err := wp.Transcribe(fileURL)
		if err != nil {
			log.Println(err)
			output = err.Error()
		}
		recorgise_duration := time.Since(start)
		output += fmt.Sprintf("\n\n%.2f seconds", recorgise_duration.Seconds())

		// Instead, prefer a context short-hand:
		return c.Send(output)
	}

	bot.Handle(telebot.OnVoice, func(c telebot.Context) error {

		fileURL, err := getFileURL(c.Bot().Token, c.Message().Voice.FileID)
		if err != nil {
			log.Println(err)
			return c.Send(err.Error())
		}
		fmt.Printf("Received voice message from %s. FileID: %s, FileURL: %s\n", c.Sender().Username, c.Message().Voice.FileID, fileURL)
		return processHandler(c, fileURL)
	})
	bot.Handle(telebot.OnVideoNote, func(c telebot.Context) error {

		fileURL, err := getFileURL(c.Bot().Token, c.Message().VideoNote.FileID)
		if err != nil {
			log.Println(err)
			return c.Send(err.Error())
		}
		fmt.Printf("Received VideoNote message from %s. FileID: %s, FileURL: %s\n", c.Sender().Username, c.Message().VideoNote.FileID, fileURL)
		return processHandler(c, fileURL)
	})
	bot.Handle(telebot.OnAudio, func(c telebot.Context) error {

		fileURL, err := getFileURL(c.Bot().Token, c.Message().Audio.FileID)
		if err != nil {
			log.Println(err)
			return c.Send(err.Error())
		}
		fmt.Printf("Received Audio message from %s. FileID: %s, FileURL: %s\n", c.Sender().Username, c.Message().Audio.FileID, fileURL)
		return processHandler(c, fileURL)
	})
	bot.Handle(telebot.OnVideo, func(c telebot.Context) error {

		fileURL, err := getFileURL(c.Bot().Token, c.Message().Video.FileID)
		if err != nil {
			log.Println(err)
			return c.Send(err.Error())
		}
		fmt.Printf("Received Video message from %s. FileID: %s, FileURL: %s\n", c.Sender().Username, c.Message().Video.FileID, fileURL)
		return processHandler(c, fileURL)
	})

	bot.Start()

}

// convertToWav converts an audio file to 16kHz WAV format
func convertToWav(input, output string) error {
	err := ffmpeg.Input(input).
		Output(output, ffmpeg.KwArgs{"c:a": "pcm_s16le", "ar": "16000", "f": "wav"}).
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
func getFileURL(botToken string, fileID string) (string, error) {
	getFileURL := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", botToken, fileID)

	resp, err := http.Get(getFileURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var file File
	err = json.Unmarshal(body, &file)
	if err != nil {
		return "", err
	}

	fileURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", botToken, file.Result.FilePath)

	return fileURL, nil
}
