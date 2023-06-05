package modeldownloader

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

///////////////////////////////////////////////////////////////////////////////
// CONSTANTS

const (
	srcUrl   = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main" // The location of the models
	srcExt   = ".bin"                                                      // Filename extension
	bufSize  = 1024 * 64                                                   // Size of the buffer used for downloading the model
	NumParts = 5
)

var (
	// The output folder. When not set, use current working directory.
	flagOut = flag.String("out", "", "Output folder")

	// HTTP timeout parameter - will timeout if takes longer than this to download a model
	flagTimeout = flag.Duration("timeout", 30*time.Minute, "HTTP timeout")

	// Quiet parameter - will not print progress if set
	flagQuiet = flag.Bool("quiet", false, "Quiet mode")
)

///////////////////////////////////////////////////////////////////////////////
// PUBLIC METHODS

// GetOut returns the path to the output directory
func GetOut() (string, error) {
	if *flagOut == "" {
		return os.Getwd()
	}
	if info, err := os.Stat(*flagOut); err != nil {
		return "", err
	} else if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", info.Name())
	} else {
		return *flagOut, nil
	}
}

// URLForModel returns the URL for the given model on huggingface.co
func URLForModel(model string) (string, error) {
	if filepath.Ext(model) != srcExt {
		model += srcExt
	}
	url, err := url.Parse(srcUrl)
	if err != nil {
		return "", err
	} else {
		url.Path = filepath.Join(url.Path, model)
	}
	return url.String(), nil
}
func Download(ctx context.Context, p io.Writer, model, out string) (string, error) {
	client := http.Client{
		Timeout: *flagTimeout,
	}

	req, err := http.NewRequest("GET", model, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", model, resp.Status)
	}

	path := filepath.Join(out, filepath.Base(model))
	info, err := os.Stat(path)

	if err == nil && info.Size() > 0 {
		fmt.Fprintln(p, "Skipping", model, "as it already exists")
		return path, nil
	}

	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	fmt.Fprintln(p, "Downloading", model, "to", out)

	partSize := resp.ContentLength / NumParts
	var wg sync.WaitGroup
	var count int64 = 0

	for i := 0; i < NumParts; i++ {
		wg.Add(1)

		start := int64(i) * partSize
		end := start + partSize

		if i == NumParts-1 {
			end = resp.ContentLength
		}

		go func(partNumber int, start int64, end int64) {
			defer wg.Done()

			req, _ := http.NewRequest("GET", model, nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
			resp, _ := client.Do(req)
			defer resp.Body.Close()

			buf := make([]byte, bufSize)
			ticker := time.NewTicker(5 * time.Second)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					// Print out progress
					progress := float64(atomic.LoadInt64(&count)) / float64(resp.ContentLength) * 100 / NumParts
					fmt.Printf("Download progress: %.2f%%\n", progress)
				default:
					n, err := resp.Body.Read(buf)
					if err != nil {
						return
					}
					_, err = file.WriteAt(buf[:n], start)
					if err != nil {
						return
					}
					atomic.AddInt64(&count, int64(n))
					start += int64(n)

				}
			}
		}(i, start, end)
	}

	wg.Wait()

	return path, nil
}

// ContextForSignal returns a context object which is cancelled when a signal
// is received. It returns nil if no signal parameter is provided
func ContextForSignal(signals ...os.Signal) context.Context {
	if len(signals) == 0 {
		return nil
	}

	ch := make(chan os.Signal)
	ctx, cancel := context.WithCancel(context.Background())

	// Send message on channel when signal received
	signal.Notify(ch, signals...)

	// When any signal received, call cancel
	go func() {
		<-ch
		cancel()
	}()

	// Return success
	return ctx
}

// Download downloads the model from the given URL to the given output directory
/*func Download(ctx context.Context, p io.Writer, model, out string) (string, error) {
	// Create HTTP client
	client := http.Client{
		Timeout: *flagTimeout,
	}

	// Initiate the download
	req, err := http.NewRequest("GET", model, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", model, resp.Status)
	}

	// If output file exists and is the same size as the model, skip
	path := filepath.Join(out, filepath.Base(model))
	if info, err := os.Stat(path); err == nil && info.Size() == resp.ContentLength {
		fmt.Fprintln(p, "Skipping", model, "as it already exists")
		return "", nil
	}

	// Create file
	w, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer w.Close()

	// Report
	fmt.Fprintln(p, "Downloading", model, "to", out)

	// Progressively download the model
	data := make([]byte, bufSize)
	count, pct := int64(0), int64(0)
	ticker := time.NewTicker(5 * time.Second)
	for {
		select {
		case <-ctx.Done():
			// Cancelled, return error
			return path, ctx.Err()
		case <-ticker.C:
			pct = DownloadReport(p, pct, count, resp.ContentLength)
		default:
			// Read body
			n, err := resp.Body.Read(data)
			if err != nil {
				DownloadReport(p, pct, count, resp.ContentLength)
				return path, err
			} else if m, err := w.Write(data[:n]); err != nil {
				return path, err
			} else {
				count += int64(m)
			}
		}
	}
}

// Report periodically reports the download progress when percentage changes
func DownloadReport(w io.Writer, pct, count, total int64) int64 {
	pct_ := count * 100 / total
	if pct_ > pct {
		fmt.Fprintf(w, "  ...%d MB written (%d%%)\n", count/1e6, pct_)
	}
	return pct_
}
*/
