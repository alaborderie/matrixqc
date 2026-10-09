package qc

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func CacheRoot() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "matrixqc"), nil
}

// Resolve existing ancestors too: a symlink must not circumvent the scratch policy.
func ValidateOutput(path string) error {
	return validateOutput(path, 0)
}

func validateOutput(path string, depth int) error {
	if depth > 40 {
		return fmt.Errorf("too many output symlinks")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	ancestor := abs
	tail := []string{}
	for {
		resolved, e := filepath.EvalSymlinks(ancestor)
		if e == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			abs = resolved
			break
		}
		if !os.IsNotExist(e) {
			return e
		}
		// EvalSymlinks returns ENOENT for a dangling link. Do not mistake that
		// link for an ordinary new filename and discard its destination.
		if info, err := os.Lstat(ancestor); err == nil && info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(ancestor)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(ancestor), target)
			}
			for i := len(tail) - 1; i >= 0; i-- {
				target = filepath.Join(target, tail[i])
			}
			return validateOutput(target, depth+1)
		}
		tail = append(tail, filepath.Base(ancestor))
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return e
		}
		ancestor = next
	}
	for _, tmp := range []string{"/tmp", "/var/tmp"} {
		if abs == tmp || strings.HasPrefix(abs, tmp+string(os.PathSeparator)) {
			return fmt.Errorf("output under %s is forbidden; use the disk-backed user cache", tmp)
		}
	}
	return nil
}

func PlanSynthetic(opts SynthOptions) (SyntheticPlan, error) {
	if opts.Variants < 8 || opts.Variants > 100 || opts.Seconds <= 0 || opts.Seconds > 60 || opts.Width < 320 || opts.Height < 240 || opts.Width > 3840 || opts.Height > 2160 || opts.Width%2 != 0 || opts.Height%2 != 0 || opts.FPS < 12 || opts.FPS > 60 {
		return SyntheticPlan{}, fmt.Errorf("synth requires 8–100 variants, 0–60 seconds, even 320–3840 x 240–2160 dimensions and 12–60 fps")
	}
	if opts.Seconds*float64(opts.FPS) < 12 {
		return SyntheticPlan{}, fmt.Errorf("synth requires at least 12 frames")
	}
	plan := SyntheticPlan{Seed: opts.Seed, Options: opts}
	rng := rand.New(rand.NewSource(opts.Seed))
	kinds := []string{"clean", "clean", "missing", "foreign_phone", "placeholder", "near_match", "single_frame_foreign_phone", "orphan"}
	fonts := []string{"DejaVuSans.ttf", "DejaVuSansMono.ttf", "DejaVuSerif.ttf"}
	for i := 0; i < opts.Variants; i++ {
		kind := "clean"
		if i < len(kinds) {
			kind = kinds[i]
		}
		width, height, aspect := opts.Width, opts.Height, "16:9"
		if i%3 == 1 {
			width = opts.Height
			height = opts.Height
			aspect = "1:1"
		}
		if i%3 == 2 {
			width = opts.Height
			height = opts.Width
			aspect = "9:16"
		}
		lang, legal := "en-GB", "Terms apply"
		if i%3 == 1 {
			lang = "fr-FR"
			legal = "Offre spéciale"
		}
		fontSize := width / 20
		if fontSize < 20 {
			fontSize = 20
		}
		v := SyntheticVariant{File: fmt.Sprintf("synthetic-%02d-%s.mp4", i, kind), VersionID: fmt.Sprintf("synthetic-region-%02d", i), Language: lang, Aspect: aspect, Phone: fmt.Sprintf("202555%04d", 100+i), URL: fmt.Sprintf("region%d.example.invalid", i), Legal: legal, ErrorKind: kind, Font: fonts[(i+rng.Intn(len(fonts)))%len(fonts)], Width: width, Height: height, FontSize: fontSize, InjectionFrame: int(opts.Seconds * float64(opts.FPS) / 2)}
		v.Label = SyntheticLabel(v)
		plan.Variants = append(plan.Variants, v)
	}
	return plan, nil
}

func SyntheticLabel(v SyntheticVariant) Label {
	label := Label{DeliverablePath: v.File, ExpectedOutcome: "pass", ExpectedChecks: []string{}, SeedKind: v.ErrorKind, Synthetic: true}
	switch v.ErrorKind {
	case "orphan":
		label.ExpectedOutcome = "error"
		label.ExpectedChecks = []string{"C0"}
	case "missing":
		label.ExpectedOutcome = "fail"
		label.ExpectedChecks = []string{"C1"}
		label.Critical = true
	case "foreign_phone":
		label.ExpectedOutcome = "fail"
		label.ExpectedChecks = []string{"C1", "C2"}
		label.Critical = true
	case "placeholder":
		label.ExpectedOutcome = "fail"
		label.ExpectedChecks = []string{"C3"}
		label.Critical = true
	case "near_match":
		label.ExpectedOutcome = "needs_review"
		label.ExpectedChecks = []string{"C4"}
		label.Critical = true
	case "single_frame_foreign_phone":
		label.ExpectedOutcome = "fail"
		label.ExpectedChecks = []string{"C2"}
		label.Critical = true
		f := v.InjectionFrame
		label.InjectionFrame = &f
	}
	return label
}

func filterPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "\\\\")
	path = strings.ReplaceAll(path, "'", "'\\''")
	path = strings.ReplaceAll(path, ":", "\\:")
	return "'" + path + "'"
}

func Synth(ctx context.Context, dir string, opts SynthOptions) error {
	plan, err := PlanSynthetic(opts)
	if err != nil {
		return err
	}
	if err := ValidateOutput(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("synthetic output directory must be empty: %s", dir)
	}
	assets := filepath.Join(dir, ".render")
	if err := os.Mkdir(assets, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(assets)
	labels := []Label{}
	records := [][]string{{"file", "version_id", "language", "phone:contact", "url:offer", "text:legal"}}
	for i, v := range plan.Variants {
		font := filepath.Join("/usr/share/fonts/truetype/dejavu", v.Font)
		if _, err := os.Stat(font); err != nil {
			return fmt.Errorf("install fonts-dejavu-core: %w", err)
		}
		// Gentle moving luminance background, deterministic at each frame index.
		filters := []string{fmt.Sprintf("geq=lum='20+2*sin(X/80+N/12+%d)':cb=128:cr=128", opts.Seed%100)}
		draw := func(name, text string, y, size int, enable string) error {
			p := filepath.Join(assets, fmt.Sprintf("%02d-%s.txt", i, name))
			if err := os.WriteFile(p, []byte(text), 0600); err != nil {
				return err
			}
			f := fmt.Sprintf("drawtext=fontfile=%s:textfile=%s:expansion=none:fontcolor=white:fontsize=%d:x=(w-text_w)/2:y=%d", filterPath(font), filterPath(p), size, y)
			if enable != "" {
				f += ":enable='" + enable + "'"
			}
			filters = append(filters, f)
			return nil
		}
		if err := draw("provenance", "SYNTHETIC", v.Height/10, v.FontSize, ""); err != nil {
			return err
		}
		phone := v.Phone
		if v.ErrorKind == "foreign_phone" {
			phone = plan.Variants[0].Phone
		}
		if v.ErrorKind == "near_match" {
			phone = phone[:len(phone)-1] + "9"
		}
		enable := ""
		if v.ErrorKind == "single_frame_foreign_phone" {
			enable = fmt.Sprintf("not(eq(n,%d))", v.InjectionFrame)
		}
		if v.ErrorKind != "missing" {
			if err := draw("phone", phone, v.Height/3, v.FontSize+4, enable); err != nil {
				return err
			}
		}
		if v.ErrorKind == "single_frame_foreign_phone" {
			if err := draw("foreign", plan.Variants[0].Phone, v.Height/3, v.FontSize+4, fmt.Sprintf("eq(n,%d)", v.InjectionFrame)); err != nil {
				return err
			}
		}
		if err := draw("url", v.URL, v.Height/2, v.FontSize-2, ""); err != nil {
			return err
		}
		if err := draw("legal", v.Legal, 2*v.Height/3, v.FontSize, ""); err != nil {
			return err
		}
		if v.ErrorKind == "placeholder" {
			if err := draw("fpo", "FPO", 4*v.Height/5, v.FontSize+8, ""); err != nil {
				return err
			}
		}
		cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", fmt.Sprintf("nullsrc=size=%dx%d:rate=%d", v.Width, v.Height, opts.FPS), "-vf", strings.Join(filters, ","), "-frames:v", fmt.Sprint(int(opts.Seconds*float64(opts.FPS))), "-an", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "16", "-threads", "2", "-filter_threads", "1", "-pix_fmt", "yuv420p", "-metadata", "comment=AI-authored synthetic test footage; not pilot or customer evidence", "-timecode", "00:00:00:00", filepath.Join(dir, v.File))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("render %s: %w: %s", v.File, err, out)
		}
		if v.ErrorKind != "orphan" {
			records = append(records, []string{v.File, v.VersionID, v.Language, v.Phone, v.URL, v.Legal})
		}
		labels = append(labels, v.Label)
	}
	f, err := os.Create(filepath.Join(dir, "synthetic-matrix.csv"))
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	err = w.WriteAll(records)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := WriteJSON(filepath.Join(dir, "synthetic-labels.json"), labels); err != nil {
		return err
	}
	return WriteJSON(filepath.Join(dir, "SYNTHETIC.json"), struct {
		Synthetic  bool          `json:"synthetic"`
		Provenance string        `json:"provenance"`
		Plan       SyntheticPlan `json:"plan"`
	}{true, "AI-authored fictional campaign and delivery matrix, procedurally rendered with ffmpeg; no shop, pilot, customer, payment or testimonial evidence", plan})
}

func WriteJSON(path string, value interface{}) error {
	if err := ValidateOutput(path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
