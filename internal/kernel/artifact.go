package kernel

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

const (
	// DefaultArtifactPreviewBytes is the default source-byte budget for artifact
	// retrieval. MaxArtifactPreviewBytes is a hard ceiling regardless of caller
	// input, so neither CLI nor MCP can accidentally flood its consumer.
	DefaultArtifactPreviewBytes = 32 << 10
	MaxArtifactPreviewBytes     = 128 << 10
	maxArtifactPreviewFiles     = adapters.MaxArtifactPreviewFiles
	maxArtifactPathBytes        = adapters.MaxArtifactPreviewPathBytes
	maxArtifactRefBytes         = 2048
)

// ArtifactPreview is the transport-neutral, bounded result returned for both a
// case-file raw blob and an fcheap stash. Ref and Content preserve the previous
// read-artifact JSON keys; the additional metadata makes truncation and binary
// encoding explicit.
type ArtifactPreview struct {
	Ref           string                 `json:"ref"`
	StashID       string                 `json:"stashId,omitempty"`
	Files         []adapters.PreviewFile `json:"files,omitempty"`
	Selected      string                 `json:"selected,omitempty"`
	Content       string                 `json:"content"`
	Encoding      string                 `json:"encoding,omitempty"` // text | base64
	Sensitive     bool                   `json:"sensitive,omitempty"`
	Truncated     bool                   `json:"truncated"`
	MaxBytes      int                    `json:"maxBytes"`
	BytesReturned int                    `json:"bytesReturned"`
}

type artifactPreviewer interface {
	PreviewWithOptions(context.Context, string, string, string, int, bool) (adapters.ArtifactPreview, error)
}

// PreviewArtifact resolves a case:// raw reference or an fcheap:// stash into a
// bounded, redacted text preview. Binary is refused by default; callers must use
// PreviewArtifactWithOptions to make a deliberate bounded-base64 request.
func (k *Kernel) PreviewArtifact(ctx context.Context, taskID, ref, selector string, maxBytes int) (ArtifactPreview, error) {
	return k.PreviewArtifactWithOptions(ctx, taskID, ref, selector, maxBytes, false)
}

// PreviewArtifactWithOptions enforces exact task ownership before retrieval.
// selector addresses one safe relative path inside an owned fcheap stash; case
// raw blobs are single objects and reject selectors.
func (k *Kernel) PreviewArtifactWithOptions(ctx context.Context, taskID, ref, selector string, maxBytes int, allowBinary bool) (ArtifactPreview, error) {
	limit := normalizeArtifactPreviewLimit(maxBytes)
	if len(ref) > maxArtifactRefBytes {
		return ArtifactPreview{}, fmt.Errorf("artifact reference exceeds %d bytes", maxArtifactRefBytes)
	}
	if strings.TrimSpace(ref) != ref {
		return ArtifactPreview{}, fmt.Errorf("artifact reference must not have surrounding whitespace")
	}
	if err := adapters.ValidateArtifactPath(selector); err != nil {
		return ArtifactPreview{}, err
	}
	if _, err := k.store.Load(taskID); err != nil {
		return ArtifactPreview{}, fmt.Errorf("%s", k.red.String(err.Error()))
	}

	switch {
	case strings.HasPrefix(ref, "case://"):
		rawID, err := ownedCaseRawID(taskID, ref)
		if err != nil {
			return ArtifactPreview{}, err
		}
		if strings.TrimSpace(selector) != "" {
			return ArtifactPreview{}, fmt.Errorf("artifact path is only valid for an fcheap stash")
		}
		raw, err := k.store.ReadRaw(taskID, rawID)
		if err != nil {
			return ArtifactPreview{}, fmt.Errorf("%s", k.red.String(err.Error()))
		}
		return k.previewBytes(k.red.String(ref), []byte(raw), limit, allowBinary)
	case strings.HasPrefix(ref, "fcheap://stash/"):
		stashID, err := fcheapStashID(ref)
		if err != nil {
			return ArtifactPreview{}, err
		}
		if err := k.requireTaskArtifactReference(taskID, ref); err != nil {
			return ArtifactPreview{}, err
		}
		return k.previewFcheapArtifact(ctx, taskID, ref, stashID, selector, limit, allowBinary)
	case strings.HasPrefix(ref, "fcheap://"):
		return ArtifactPreview{}, fmt.Errorf("unrecognized fcheap artifact reference %q", k.red.String(ref))
	default:
		return ArtifactPreview{}, fmt.Errorf("unrecognized artifact reference %q", k.red.String(ref))
	}
}

func ownedCaseRawID(taskID, ref string) (string, error) {
	prefix := "case://" + taskID + "/raw/"
	if !strings.HasPrefix(ref, prefix) {
		if strings.HasPrefix(ref, "case://"+taskID+"/evidence/") {
			return "", fmt.Errorf("this evidence has no stored raw output (the reference self-points)")
		}
		return "", fmt.Errorf("case artifact reference must belong to task %s and use case://%s/raw/<rawId>", taskID, taskID)
	}
	rawID := strings.TrimPrefix(ref, prefix)
	if err := adapters.ValidateArtifactID(rawID); err != nil {
		return "", fmt.Errorf("invalid case raw id: %w", err)
	}
	if ref != prefix+rawID {
		return "", fmt.Errorf("case artifact reference is not canonical")
	}
	return rawID, nil
}

func fcheapStashID(ref string) (string, error) {
	const prefix = "fcheap://stash/"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("fcheap artifact reference must use fcheap://stash/<stashId>")
	}
	stashID := strings.TrimPrefix(ref, prefix)
	if err := adapters.ValidateArtifactID(stashID); err != nil {
		return "", fmt.Errorf("invalid fcheap stash id: %w", err)
	}
	if ref != prefix+stashID {
		return "", fmt.Errorf("fcheap artifact reference is not canonical")
	}
	return stashID, nil
}

func (k *Kernel) requireTaskArtifactReference(taskID, ref string) error {
	evidence, err := k.store.Evidence(taskID)
	if err != nil {
		return fmt.Errorf("load artifact evidence: %s", k.red.String(err.Error()))
	}
	for _, item := range evidence {
		if item.Kind == domain.KindArtifact && (item.RawRef == ref || item.Source.URI == ref) {
			return nil
		}
	}
	receipts, err := k.store.Verifications(taskID)
	if err != nil {
		return fmt.Errorf("load artifact receipts: %s", k.red.String(err.Error()))
	}
	for _, receipt := range receipts {
		if receipt.Artifact == ref {
			return nil
		}
	}
	return fmt.Errorf("fcheap artifact is not referenced by task %s evidence or verification receipts", taskID)
}

func (k *Kernel) previewFcheapArtifact(ctx context.Context, taskID, ref, stashID, selector string, limit int, allowBinary bool) (ArtifactPreview, error) {
	adapter := k.reg.Get("fcheap")
	previewer, ok := adapter.(artifactPreviewer)
	if !ok {
		return ArtifactPreview{}, fmt.Errorf("fcheap artifact preview is unavailable")
	}
	started := k.now()
	preview, err := previewer.PreviewWithOptions(ctx, k.cfg.Workspace, ref, selector, limit, allowBinary)
	status := adapters.StatusAuthoritative
	note := ""
	if err != nil {
		status = adapters.StatusError
		if errors.Is(err, adapters.ErrToolMissing) {
			status = adapters.StatusUnavailable
		}
		note = clipStr(k.red.String(err.Error()), 120)
	}
	k.recordCommand(taskID, "fcheap", "preview", domain.ActionReadOnly, status, started, note)
	if err != nil {
		return ArtifactPreview{}, fmt.Errorf("artifact preview failed: %s", k.red.String(err.Error()))
	}

	if preview.StashID != stashID {
		return ArtifactPreview{}, fmt.Errorf("artifact preview returned a different stash id")
	}
	if err := adapters.ValidateArtifactID(preview.StashID); err != nil {
		return ArtifactPreview{}, fmt.Errorf("artifact preview returned invalid stash id: %w", err)
	}
	selected := k.red.String(preview.Selected)
	if err := adapters.ValidateArtifactPath(selected); err != nil {
		return ArtifactPreview{}, fmt.Errorf("artifact preview returned unsafe selected path: %w", err)
	}
	if selector != "" && selected != selector {
		return ArtifactPreview{}, fmt.Errorf("artifact preview selected %q instead of requested path", selected)
	}
	result := ArtifactPreview{
		Ref: k.red.String(ref), StashID: preview.StashID, Selected: selected,
		Encoding: preview.Encoding, Truncated: preview.Truncated, MaxBytes: limit,
	}
	result.Files, err = k.validatedPreviewFiles(preview.Files)
	if err != nil {
		return ArtifactPreview{}, err
	}
	if selected != "" && !previewFilesContain(result.Files, selected) {
		return ArtifactPreview{}, fmt.Errorf("artifact preview selected path is missing from its file list")
	}
	content, encoding, truncated, bytesReturned, err := k.boundPreviewContent(preview.Content, preview.Encoding, limit, allowBinary)
	if err != nil {
		return ArtifactPreview{}, err
	}
	result.Content = content
	result.Encoding = encoding
	result.Sensitive = encoding == "base64" || strings.Contains(content, "«redacted»")
	result.Truncated = result.Truncated || truncated
	result.BytesReturned = bytesReturned
	return result, nil
}

// ReadArtifact preserves the original string-returning kernel API. New callers
// should use PreviewArtifact so they also receive truncation/encoding metadata.
func (k *Kernel) ReadArtifact(taskID, ref string) (string, error) {
	preview, err := k.PreviewArtifact(context.Background(), taskID, ref, "", DefaultArtifactPreviewBytes)
	if err != nil {
		return "", err
	}
	return preview.Content, nil
}

func (k *Kernel) previewBytes(ref string, data []byte, limit int, allowBinary bool) (ArtifactPreview, error) {
	binary := adapters.ArtifactContentIsBinary(data)
	if binary && !allowBinary {
		return ArtifactPreview{}, fmt.Errorf("artifact content is binary; retry with explicit binary permission")
	}
	if !binary {
		sensitive := k.red.Detected(string(data))
		redacted := k.red.String(string(data))
		bounded, truncated := boundedUTF8(redacted, limit)
		preview := ArtifactPreview{
			Ref: ref, Content: bounded, Encoding: "text", Truncated: truncated,
			Sensitive: sensitive || strings.Contains(redacted, "«redacted»"), MaxBytes: limit,
		}
		preview.BytesReturned = len(preview.Content)
		return preview, nil
	}
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}
	preview := ArtifactPreview{
		Ref: ref, Encoding: "base64", Truncated: truncated,
		Sensitive: true, MaxBytes: limit, BytesReturned: len(data),
	}
	preview.Content = base64.StdEncoding.EncodeToString(data)
	return preview, nil
}

func (k *Kernel) boundPreviewContent(content, encoding string, limit int, allowBinary bool) (string, string, bool, int, error) {
	switch encoding {
	case "", "text":
		data := []byte(content)
		if adapters.ArtifactContentIsBinary(data) {
			if !allowBinary {
				return "", "", false, 0, fmt.Errorf("artifact content is binary; retry with explicit binary permission")
			}
			truncated := len(data) > limit
			if truncated {
				data = data[:limit]
			}
			return base64.StdEncoding.EncodeToString(data), "base64", truncated, len(data), nil
		}
		content = k.red.String(content)
		bounded, truncated := boundedUTF8(content, limit)
		return bounded, "text", truncated, len(bounded), nil
	case "base64":
		if !allowBinary {
			return "", "", false, 0, fmt.Errorf("artifact content is binary; retry with explicit binary permission")
		}
		maxEncoded := base64.StdEncoding.EncodedLen(limit)
		inputTruncated := len(content) > maxEncoded
		if inputTruncated {
			content = content[:maxEncoded]
		}
		data, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return "", "", false, 0, fmt.Errorf("artifact preview returned invalid base64 content")
		}
		truncated := inputTruncated || len(data) > limit
		if truncated {
			data = data[:limit]
		}
		return base64.StdEncoding.EncodeToString(data), "base64", truncated, len(data), nil
	default:
		bounded, _ := boundedUTF8(encoding, 64)
		return "", "", false, 0, fmt.Errorf("artifact preview returned unsupported encoding %q", bounded)
	}
}

func normalizeArtifactPreviewLimit(maxBytes int) int {
	if maxBytes <= 0 {
		return DefaultArtifactPreviewBytes
	}
	if maxBytes > MaxArtifactPreviewBytes {
		return MaxArtifactPreviewBytes
	}
	return maxBytes
}

func (k *Kernel) validatedPreviewFiles(files []adapters.PreviewFile) ([]adapters.PreviewFile, error) {
	if len(files) > maxArtifactPreviewFiles {
		return nil, fmt.Errorf("artifact preview returned more than %d files", maxArtifactPreviewFiles)
	}
	out := make([]adapters.PreviewFile, 0, len(files))
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		artifactPath := k.red.String(file.Path)
		if err := adapters.ValidateArtifactPath(artifactPath); err != nil {
			return nil, fmt.Errorf("artifact preview returned unsafe file path: %w", err)
		}
		if seen[artifactPath] {
			return nil, fmt.Errorf("artifact preview returned duplicate file path %q", artifactPath)
		}
		if file.Size < 0 {
			return nil, fmt.Errorf("artifact preview returned a negative file size")
		}
		seen[artifactPath] = true
		out = append(out, adapters.PreviewFile{Path: artifactPath, Size: file.Size})
	}
	return out, nil
}

func previewFilesContain(files []adapters.PreviewFile, selected string) bool {
	for _, file := range files {
		if file.Path == selected {
			return true
		}
	}
	return false
}

func boundedUTF8(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	data := []byte(value)[:maxBytes]
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data), true
}
