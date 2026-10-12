package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/alaborderie/a-1006-122137/internal/qc"
)

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: matrixqc synth|check|seed|bench|serve [flags]")
	}
	var root string
	if args[0] != "serve" {
		var err error
		root, err = qc.CacheRoot()
		if err != nil {
			return err
		}
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
	case "check":
		f := flag.NewFlagSet("check", flag.ContinueOnError)
		opts := checkFlags(f, root)
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if err := validateCheck(*opts); err != nil {
			return err
		}
		batch, err := qc.Check(ctx, *opts)
		if err != nil {
			return err
		}
		printSummary(batch, opts.OutDir)
		return nil
	case "bench":
		f := flag.NewFlagSet("bench", flag.ContinueOnError)
		opts := checkFlags(f, root)
		results := f.String("results", "", "comma-separated existing results.json files; otherwise run check")
		labelsPath := f.String("labels", "", "independently labelled JSON cases")
		out := f.String("benchmark", filepath.Join(root, "BENCHMARK.md"), "benchmark markdown output")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if err := qc.ValidateOutput(*out); err != nil {
			return err
		}
		if *labelsPath == "" {
			return fmt.Errorf("--labels is required")
		}
		data, err := os.ReadFile(*labelsPath)
		if err != nil {
			return err
		}
		var labels []qc.Label
		if err := json.Unmarshal(data, &labels); err != nil {
			return err
		}
		batches := []qc.Batch{}
		if *results == "" {
			if err := validateCheck(*opts); err != nil {
				return err
			}
			batch, err := qc.Check(ctx, *opts)
			if err != nil {
				return err
			}
			batches = append(batches, batch)
		} else {
			for _, path := range strings.Split(*results, ",") {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				var b qc.Batch
				if err := json.Unmarshal(data, &b); err != nil {
					return err
				}
				batches = append(batches, b)
			}
		}
		metrics := []qc.BenchmarkMetrics{}
		for _, batch := range batches {
			metric, err := qc.ScoreBenchmark(batch, labels)
			if err != nil {
				return err
			}
			metrics = append(metrics, metric)
		}
		var text strings.Builder
		if err := qc.WriteBenchmark(&text, metrics); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*out), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(*out, []byte(text.String()), 0644); err != nil {
			return err
		}
		fmt.Printf("Benchmark written: %s\n", *out)
		return nil
	case "seed":
		return runSeed(ctx, args[1:], root)
	case "serve":
		f := flag.NewFlagSet("serve", flag.ContinueOnError)
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		addr := f.String("addr", ":"+port, "listen address (default from PORT or :8080)")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		site, err := qc.NewSite(qc.SiteConfig{PilotURL: os.Getenv("PILOT_URL"), ContactEmail: os.Getenv("CONTACT_EMAIL")})
		if err != nil {
			return err
		}
		server := &http.Server{Addr: *addr, Handler: site, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server.Shutdown(shutdownCtx)
		}()
		fmt.Printf("Synthetic-only demo listening on %s\n", *addr)
		err = server.ListenAndServe()
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func checkFlags(f *flag.FlagSet, root string) *qc.CheckOptions {
	opts := &qc.CheckOptions{}
	f.StringVar(&opts.MatrixPath, "matrix", "", "matrix CSV path")
	f.StringVar(&opts.FilesDir, "files", "", "local deliverable directory")
	f.StringVar(&opts.OutDir, "out", filepath.Join(root, "report"), "results and HTML output directory")
	f.StringVar(&opts.Engine, "engine", "rapidocr", "rapidocr or tesseract")
	f.IntVar(&opts.Match.MinFrames, "min-frames", 12, "required consecutive frames")
	f.Float64Var(&opts.Match.Confidence, "confidence", .8, "minimum confidence for an exact pass")
	f.Func("placeholder", "additional campaign placeholder token (repeatable)", func(value string) error {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("placeholder token cannot be blank")
		}
		opts.Match.PlaceholderTokens = append(opts.Match.PlaceholderTokens, value)
		return nil
	})
	f.BoolVar(&opts.Pipeline.NoGate, "no-gate", false, "OCR every decoded frame (baseline)")
	f.Float64Var(&opts.Pipeline.Threshold, "gate-threshold", 1, "per-tile mean absolute difference threshold")
	return opts
}
func validateCheck(opts qc.CheckOptions) error {
	if opts.MatrixPath == "" || opts.FilesDir == "" {
		return fmt.Errorf("--matrix and --files are required")
	}
	if opts.Match.MinFrames < 1 || opts.Match.Confidence <= 0 || opts.Match.Confidence > 1 || math.IsNaN(opts.Match.Confidence) || math.IsInf(opts.Match.Confidence, 0) {
		return fmt.Errorf("--min-frames must be positive and --confidence finite in (0,1]")
	}
	if opts.Pipeline.Threshold < 0 || math.IsNaN(opts.Pipeline.Threshold) || math.IsInf(opts.Pipeline.Threshold, 0) {
		return fmt.Errorf("--gate-threshold must be finite and non-negative")
	}
	return nil
}
func printSummary(batch qc.Batch, dir string) {
	counts := map[string]int{}
	for _, d := range batch.Deliverables {
		counts[d.Outcome]++
	}
	fmt.Printf("Report written: %s (pass %d, fail %d, needs_review %d, error %d)\n", dir, counts["pass"], counts["fail"], counts["needs_review"], counts["error"])
}

func runSeed(ctx context.Context, args []string, root string) error {
	f := flag.NewFlagSet("seed", flag.ContinueOnError)
	matrixPath := f.String("matrix", "", "source matrix CSV")
	files := f.String("files", "", "source deliverable directory (required for splice)")
	out := f.String("out", filepath.Join(root, "seeded"), "empty disk-backed scratch output directory")
	splice := f.Bool("splice", false, "replace one frame with a sibling frame, in lossless scratch footage")
	sourceA := f.String("source-a", "", "target filename in the matrix")
	sourceB := f.String("source-b", "", "sibling filename")
	frame := f.Int("frame", 1, "zero-based frame to replace")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *matrixPath == "" {
		return fmt.Errorf("--matrix is required")
	}
	if err := qc.ValidateOutput(*out); err != nil {
		return err
	}
	data, err := os.ReadFile(*matrixPath)
	if err != nil {
		return err
	}
	matrix, err := qc.ParseMatrix(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(*out)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("seed output directory must be empty")
	}
	var labels []qc.Label
	var mutated []byte
	sourceSynthetic := qc.SourceSynthetic(*files)
	if !*splice {
		mutated, labels, err = qc.SeedMatrix(data, sourceSynthetic)
		if err != nil {
			return err
		}
	} else {
		if *files == "" || filepath.Base(*sourceA) != *sourceA || filepath.Base(*sourceB) != *sourceB || *sourceA == "" || *sourceB == "" || *sourceA == *sourceB {
			return fmt.Errorf("splice requires --files and two different basenames --source-a, --source-b")
		}
		records, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
		if err != nil {
			return err
		}
		fileColumn := 0
		for i, h := range records[0] {
			if h == "file" {
				fileColumn = i
			}
		}
		found := false
		for _, row := range matrix.Rows {
			if row.FilePattern == *sourceA {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("--source-a must have an exact matrix row")
		}
		splicedName := strings.TrimSuffix(*sourceA, filepath.Ext(*sourceA)) + "-spliced.mkv"
		if err := qc.Splice(ctx, filepath.Join(*files, *sourceA), filepath.Join(*files, *sourceB), filepath.Join(*out, splicedName), *frame); err != nil {
			return err
		}
		for i, row := range matrix.Rows {
			label := qc.Label{DeliverablePath: row.FilePattern, ExpectedOutcome: "pass", ExpectedChecks: []string{}, Synthetic: sourceSynthetic}
			if row.FilePattern == *sourceA {
				records[i+1][fileColumn] = splicedName
				label.DeliverablePath = splicedName
				label.ExpectedOutcome = "fail"
				label.ExpectedChecks = []string{"C2"}
				label.Critical = true
				label.SeedKind = "single_frame_sibling"
				injection := *frame
				label.InjectionFrame = &injection
			} else {
				if strings.ContainsAny(row.FilePattern, "*?[") {
					return fmt.Errorf("splice seeding requires exact filenames, not globs")
				}
				source, err := filepath.Abs(filepath.Join(*files, row.FilePattern))
				if err != nil {
					return err
				}
				if err := os.Symlink(source, filepath.Join(*out, row.FilePattern)); err != nil {
					return err
				}
			}
			labels = append(labels, label)
		}
		var csvText strings.Builder
		writer := csv.NewWriter(&csvText)
		if err := writer.WriteAll(records); err != nil {
			return err
		}
		mutated = []byte(csvText.String())
	}
	if err := os.WriteFile(filepath.Join(*out, "seeded-matrix.csv"), mutated, 0644); err != nil {
		return err
	}
	if err := qc.WriteJSON(filepath.Join(*out, "seeded-labels.json"), labels); err != nil {
		return err
	}
	allSynthetic := true
	for _, label := range labels {
		allSynthetic = allSynthetic && label.Synthetic
	}
	if err := qc.WriteJSON(filepath.Join(*out, "SYNTHETIC.json"), struct {
		Synthetic bool   `json:"synthetic"`
		Note      string `json:"note"`
	}{allSynthetic, "Injected test expectations, not customer or pilot evidence. Labels assume originals conform; verify originals independently before benchmarking. Blank cells remove expectations, so missing legal is seeded by requiring a known absent string."}); err != nil {
		return err
	}
	fmt.Printf("Seeded test matrix and labels: %s; originals unchanged\n", *out)
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
