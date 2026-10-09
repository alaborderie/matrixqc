package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/alaborderie/a-1006-122137/internal/qc"
)

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: matrixqc synth|check|seed|bench|serve [flags]")
	}
	root, err := qc.CacheRoot()
	if err != nil {
		return err
	}
	switch args[0] {
	case "synth":
		f := flag.NewFlagSet("synth", flag.ContinueOnError)
		dir := f.String("out", filepath.Join(root, "synthetic-campaign"), "empty directory for synthetic files")
		seed := f.Int64("seed", 42, "deterministic synthetic seed")
		variants := f.Int("variants", 8, "synthetic variants (at least 8)")
		seconds := f.Float64("seconds", 5, "duration of each synthetic video")
		width := f.Int("width", 854, "landscape width; square/portrait derived from these dimensions")
		height := f.Int("height", 480, "landscape height")
		fps := f.Int("fps", 24, "frames per second")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return fmt.Errorf("unexpected synth arguments")
		}
		if err := qc.Synth(ctx, *dir, qc.SynthOptions{Seed: *seed, Variants: *variants, Seconds: *seconds, Width: *width, Height: *height, FPS: *fps}); err != nil {
			return err
		}
		fmt.Printf("Synthetic campaign only: %s\n", *dir)
		return nil
	case "check", "seed", "bench", "serve":
		return fmt.Errorf("%s is not implemented yet", args[0])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
