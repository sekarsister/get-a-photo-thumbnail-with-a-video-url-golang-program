package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

const (
	defaultSeekTime = "00:00:01"
	chunkSize       = 2 * 1024 * 1024
)

func main() {
	var (
		videoURL   = flag.String("url", "", "URL video")
		outputFile = flag.String("out", "", "Nama file output")
		seekTime   = flag.String("time", defaultSeekTime, "Waktu frame")
		timeout    = flag.Duration("timeout", 60*time.Second, "Batas waktu")
	)
	flag.Parse()

	if *videoURL == "" {
		fmt.Print("Masukkan URL video: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			*videoURL = strings.TrimSpace(scanner.Text())
		}
		if *videoURL == "" {
			log.Fatal("URL video tidak boleh kosong.")
		}
	}

	if !strings.HasPrefix(*videoURL, "http://") && !strings.HasPrefix(*videoURL, "https://") {
		log.Fatal("URL harus diawali http:// atau https://")
	}

	if *outputFile == "" {
		*outputFile = generateOutputName(*videoURL)
	}

	fmt.Printf("URL Video    : %s\n", *videoURL)
	fmt.Printf("File Output  : %s\n", *outputFile)
	fmt.Printf("Posisi Frame : %s\n", *seekTime)
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := tryDirectThumbnail(ctx, *videoURL, *outputFile); err == nil {
		fmt.Printf("✅ Thumbnail langsung berhasil diunduh ke %s\n", *outputFile)
		return
	}

	if err := streamExtract(ctx, *videoURL, *outputFile, *seekTime); err == nil {
		fmt.Printf("✅ Thumbnail berhasil diekstrak via streaming ke %s\n", *outputFile)
		return
	}

	log.Println("Streaming gagal, mencoba potongan awal...")
	if err := partialDownloadExtract(ctx, *videoURL, *outputFile, *seekTime); err != nil {
		log.Fatalf("Gagal mengambil thumbnail: %v", err)
	}
	fmt.Printf("✅ Thumbnail berhasil dari potongan awal ke %s\n", *outputFile)
}

func tryDirectThumbnail(ctx context.Context, videoURL, outputPath string) error {
	thumbURL := strings.Replace(videoURL, ".mp4", ".jpg", 1)
	if thumbURL == videoURL {
		return fmt.Errorf("tidak dapat menebak URL thumbnail")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", thumbURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return fmt.Errorf("thumbnail langsung tidak tersedia")
	}
	defer resp.Body.Close()

	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		return fmt.Errorf("konten bukan gambar")
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func streamExtract(ctx context.Context, videoURL, outputPath, seekTime string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg tidak ditemukan: %w", err)
	}

	cmd := exec.CommandContext(ctx,
		"ffmpeg",
		"-y",
		"-i", videoURL,
		"-ss", seekTime,
		"-vframes", "1",
		"-q:v", "2",
		outputPath,
	)
	return cmd.Run()
}

func partialDownloadExtract(ctx context.Context, videoURL, outputPath, seekTime string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg tidak ditemukan: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", videoURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", chunkSize-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server tidak mendukung partial download (status %d)", resp.StatusCode)
	}

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		io.Copy(pw, resp.Body)
	}()

	cmd := exec.CommandContext(ctx,
		"ffmpeg",
		"-y",
		"-i", "pipe:0",
		"-ss", seekTime,
		"-vframes", "1",
		"-q:v", "2",
		outputPath,
	)
	cmd.Stdin = pr

	var errBuf strings.Builder
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg gagal: %w, detail: %s", err, errBuf.String())
	}
	return nil
}

func generateOutputName(rawURL string) string {
	base := path.Base(rawURL)
	if idx := strings.Index(base, "?"); idx != -1 {
		base = base[:idx]
	}
	ext := path.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name == "" {
		name = "thumbnail"
	}
	return fmt.Sprintf("thumbnail_%s.jpg", name)
}
