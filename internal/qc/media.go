package qc

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

type MediaInfo struct {
	Container     string  `json:"container"`
	Codec         string  `json:"codec"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	FPS           float64 `json:"fps"`
	FrameCount    int     `json:"frame_count"`
	StartTimecode string  `json:"start_timecode"`
}
type Label struct {
	DeliverablePath string   `json:"deliverable_path"`
	ExpectedOutcome string   `json:"expected_outcome"`
	ExpectedChecks  []string `json:"expected_checks"`
	SeedKind        string   `json:"seed_kind"`
	Critical        bool     `json:"critical"`
	Synthetic       bool     `json:"synthetic"`
	InjectionFrame  *int     `json:"injection_frame,omitempty"`
}
type SynthOptions struct {
	Seed               int64
	Variants           int
	Seconds            float64
	Width, Height, FPS int
}
type SyntheticVariant struct {
	File, VersionID, Language, Aspect, Phone, URL, Legal, ErrorKind, Font string
	Width, Height, FontSize, InjectionFrame                               int
	Label                                                                 Label
}
type SyntheticPlan struct {
	Seed     int64
	Variants []SyntheticVariant
	Options  SynthOptions
}

func ParseProbe(data []byte) (MediaInfo, error) {
	var raw struct {
		Streams []struct {
			CodecType   string `json:"codec_type"`
			CodecName   string `json:"codec_name"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			AverageRate string `json:"avg_frame_rate"`
			ReadFrames  string `json:"nb_read_frames"`
			Frames      string `json:"nb_frames"`
			Tags        struct {
				Timecode string `json:"timecode"`
			} `json:"tags"`
		} `json:"streams"`
		Format struct {
			Name string `json:"format_name"`
			Tags struct {
				Timecode string `json:"timecode"`
			} `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return MediaInfo{}, fmt.Errorf("ffprobe JSON: %w", err)
	}
	for _, s := range raw.Streams {
		if s.CodecType == "video" {
			var num, den float64
			_, err := fmt.Sscanf(s.AverageRate, "%f/%f", &num, &den)
			count, _ := strconv.Atoi(s.ReadFrames)
			if count == 0 {
				count, _ = strconv.Atoi(s.Frames)
			}
			if err != nil || num <= 0 || den <= 0 || count <= 0 || s.Width <= 0 || s.Height <= 0 {
				return MediaInfo{}, fmt.Errorf("unsupported video facts: dimensions, frame rate and frame count required")
			}
			tc := s.Tags.Timecode
			if tc == "" {
				tc = raw.Format.Tags.Timecode
			}
			if tc == "" {
				tc = "00:00:00:00"
			}
			return MediaInfo{Container: raw.Format.Name, Codec: s.CodecName, Width: s.Width, Height: s.Height, FPS: num / den, FrameCount: count, StartTimecode: tc}, nil
		}
	}
	return MediaInfo{}, fmt.Errorf("no video stream")
}

func Probe(ctx context.Context, path string) (MediaInfo, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-count_frames", "-show_streams", "-show_format", "-of", "json", path)
	data, err := cmd.Output()
	if err != nil {
		return MediaInfo{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	return ParseProbe(data)
}
