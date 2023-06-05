package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"

	//"io"
	"os"
	"time"

	// Package imports
	whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	wav "github.com/go-audio/wav"
	// wav "github.com/go-audio/wav"
	// "github.com/go-delve/delve/pkg/terminal/colorize"
	"github.com/imdario/mergo"
)

type WhisperProcessor struct {
	model   whisper.Model
	context whisper.Context
	params  WhisperParams
}

type WhisperParams struct {
	language   string
	no_context bool
	translate  bool
	offset     time.Duration
	duration   time.Duration
	threads    uint
	speedup    bool
	max_len    uint
	max_tokens uint
	word_thold float64
	tokens     bool
	colorize   bool
	out        string
}

func WPInit() *WhisperProcessor {
	return &WhisperProcessor{
		params: WhisperParams{
			language:   "auto",
			no_context: true,
			translate:  false,
			offset:     0,
			duration:   0,
			threads:    0,
			speedup:    false,
			max_len:    0,
			word_thold: 0,
			tokens:     false,
			colorize:   false,
			out:        "",
		},
	}
}
func (wp *WhisperProcessor) LoadModel(modelfile string) (err error) {
	wp.model, err = whisper.New(modelfile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	//defer wp.model.Close()

	return err
}

func (wp *WhisperProcessor) PrepareModel(newparams WhisperParams) (err error) {
	// Set the parameters
	params := wp.params
	if err := mergo.Merge(&params, newparams, mergo.WithOverride); err != nil {
		return err
	}

	wp.params = params

	wp.context, err = wp.model.NewContext()
	fmt.Printf("Setting language to %q\n", wp.params.language)
	if err := wp.context.SetLanguage(wp.params.language); err != nil {
		return err
	}
	fmt.Printf("Setting translate to %v\n", wp.params.translate)
	wp.context.SetTranslate(wp.params.translate)
	if wp.params.offset != 0 {
		fmt.Printf("Setting offset to %v\n", wp.params.offset)
		wp.context.SetOffset(wp.params.offset)
	}
	if wp.params.duration != 0 {
		fmt.Printf("Setting duration to %v\n", wp.params.duration)
		wp.context.SetDuration(wp.params.duration)
	}
	fmt.Printf("Setting speedup to %v\n", wp.params.speedup)
	wp.context.SetSpeedup(wp.params.speedup)
	fmt.Printf("Setting no_context to %v\n", wp.params.no_context)
	wp.context.SetNoContext(wp.params.no_context)
	if wp.params.threads != 0 {
		fmt.Printf("Setting threads to %v\n", wp.params.threads)
		wp.context.SetThreads(wp.params.threads)
	}
	if wp.params.max_len != 0 {
		fmt.Printf("Setting max_len to %v\n", wp.params.max_len)
		wp.context.SetMaxSegmentLength(wp.params.max_len)
	}
	if wp.params.max_tokens != 0 {
		fmt.Printf("Setting max_tokens to %v\n", wp.params.max_tokens)
		wp.context.SetMaxTokensPerSegment(wp.params.max_tokens)
	}
	if wp.params.word_thold != 0 {
		fmt.Printf("Setting word_threshold to %v\n", wp.params.word_thold)
		wp.context.SetTokenThreshold(float32(wp.params.word_thold))
	}

	fmt.Printf("\n%s\n", wp.context.SystemInfo())

	return err
}

func (wp *WhisperProcessor) Transcribe(file string) (output string, err error) {
	var data []float32
	var cb whisper.SegmentCallback

	tmpfile := tempFileName("", ".wav")
	// Convert the received audio to 16kHz WAV format
	err = convertToWav(file, tmpfile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return "", err
	}

	output = ""

	// Open the file
	fmt.Printf("Loading %q\n", tmpfile)
	fh, err := os.Open(tmpfile)
	if err != nil {
		fmt.Println(err)
		return "", err
	}
	defer fh.Close()

	// Decode the WAV file - load the full buffer
	dec := wav.NewDecoder(fh)
	if buf, err := dec.FullPCMBuffer(); err != nil {
		fmt.Println(err)
		return "", err
	} else if dec.SampleRate != whisper.SampleRate {
		fmt.Println(fmt.Errorf("unsupported sample rate: %d", dec.SampleRate))
		return "", err
	} else if dec.NumChans != 1 {
		fmt.Println(fmt.Errorf("unsupported number of channels: %d", dec.NumChans))
		return "", err
	} else {
		data = buf.AsFloat32Buffer().Data
	}

	// Process the data
	fmt.Printf("  ...processing %q\n", tmpfile)
	wp.context.ResetTimings()
	if err := wp.context.Process(data, cb); err != nil {
		fmt.Println(err)
		return "", err
	}

	wp.context.PrintTimings()

	for {
		segment, err := wp.context.NextSegment()
		if err == io.EOF {
			break
		} else if err != nil {
			break
		}
		fmt.Printf("[%6s->%6s]", segment.Start.Truncate(time.Millisecond), segment.End.Truncate(time.Millisecond))

		fmt.Println(" ", segment.Text)

		output += segment.Text

	}

	os.Remove(tmpfile)

	return output, err
}

func tempFileName(prefix, suffix string) string {
	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	return filepath.Join(os.TempDir(), prefix+hex.EncodeToString(randBytes)+suffix)
}

/*
func Process(context whisper.Context, path string, flags *Flags) (string, error) {
	var data []float32

	// Open the file
	fmt.Fprintf(flags.Output(), "Loading %q\n", path)
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer fh.Close()

	// Decode the WAV file - load the full buffer
	dec := wav.NewDecoder(fh)
	if buf, err := dec.FullPCMBuffer(); err != nil {
		return "", err
	} else if dec.SampleRate != whisper.SampleRate {
		return "", fmt.Errorf("unsupported sample rate: %d", dec.SampleRate)
	} else if dec.NumChans != 1 {
		return "", fmt.Errorf("unsupported number of channels: %d", dec.NumChans)
	} else {
		data = buf.AsFloat32Buffer().Data
	}

	// Segment callback when -tokens is specified
	var cb whisper.SegmentCallback
	if flags.IsTokens() {
		cb = func(segment whisper.Segment) {
			fmt.Fprintf(flags.Output(), "%02d [%6s->%6s] ", segment.Num, segment.Start.Truncate(time.Millisecond), segment.End.Truncate(time.Millisecond))
			for _, token := range segment.Tokens {

				fmt.Fprint(flags.Output(), token.Text, " ")

			}
			fmt.Fprintln(flags.Output(), "")
			fmt.Fprintln(flags.Output(), "")
		}
	}

	// Process the data
	fmt.Fprintf(flags.Output(), "  ...processing %q\n", path)
	context.ResetTimings()
	if err := context.Process(data, cb); err != nil {
		return "", err
	}

	context.PrintTimings()

	return Output(os.Stdout, context)
}

// Output text to terminal
func Output(w io.Writer, context whisper.Context) (string, error) {
	result := ""
	for {
		segment, err := context.NextSegment()
		if err == io.EOF {
			return result, nil
		} else if err != nil {
			return "", err
		}
		fmt.Fprintf(w, "[%6s->%6s]", segment.Start.Truncate(time.Millisecond), segment.End.Truncate(time.Millisecond))

		fmt.Fprintln(w, " ", segment.Text)

		result += segment.Text

	}

}
*/
