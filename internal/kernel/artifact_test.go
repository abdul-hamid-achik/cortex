package kernel

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

type previewingFcheap struct {
	preview     adapters.ArtifactPreview
	err         error
	gotDir      string
	gotStash    string
	gotSelector string
	gotMaxBytes int
	gotBinary   bool
	calls       int
}

func (f *previewingFcheap) Name() string { return "fcheap" }
func (f *previewingFcheap) Capabilities() []adapters.Capability {
	return []adapters.Capability{adapters.CapabilityArtifacts}
}
func (f *previewingFcheap) Health(context.Context) error { return nil }
func (f *previewingFcheap) Execute(context.Context, adapters.Request) (adapters.Result, error) {
	return adapters.Result{Tool: "fcheap", Status: adapters.StatusAuthoritative}, nil
}
func (f *previewingFcheap) PreviewWithOptions(_ context.Context, dir, stash, selector string, maxBytes int, allowBinary bool) (adapters.ArtifactPreview, error) {
	f.gotDir, f.gotStash, f.gotSelector, f.gotMaxBytes = dir, stash, selector, maxBytes
	f.gotBinary = allowBinary
	f.calls++
	return f.preview, f.err
}

func referenceFcheapEvidence(t *testing.T, k *Kernel, taskID, ref string) {
	t.Helper()
	err := k.Store().AppendEvidence(taskID, domain.Evidence{
		ID: "ev_artifact_" + strings.TrimPrefix(ref, "fcheap://stash/"), Timestamp: time.Now().UTC(),
		Kind: domain.KindArtifact, Source: domain.Source{Tool: "fcheap", URI: ref},
		Claim: "task-owned artifact", Confidence: domain.ConfidenceHigh, RawRef: ref,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPreviewCaseArtifactIsBoundedAndRedacted(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, err := k.StartTask(context.Background(), StartInput{Goal: "preview raw evidence"})
	if err != nil {
		t.Fatal(err)
	}
	secret := "supersecretvalue"
	raw := "API_KEY=" + secret + "\n" + strings.Repeat("visible", 20)
	if err := k.Store().WriteRaw(started.TaskID, "raw_text", raw); err != nil {
		t.Fatal(err)
	}
	ref := "case://" + started.TaskID + "/raw/raw_text"
	preview, err := k.PreviewArtifact(context.Background(), started.TaskID, ref, "", 32)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Ref != ref || preview.Encoding != "text" || !preview.Truncated || !preview.Sensitive || preview.MaxBytes != 32 {
		t.Fatalf("preview metadata = %+v", preview)
	}
	if len(preview.Content) > 32 || strings.Contains(preview.Content, secret) || !strings.Contains(preview.Content, "redacted") {
		t.Fatalf("preview must be bounded and redacted: %+v", preview)
	}
}

func TestPreviewCaseArtifactRefusesBinaryUnlessExplicitlyAllowed(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "preview binary evidence"})
	if err := k.Store().WriteRaw(started.TaskID, "raw_bin", string([]byte{0xff, 0x00, 0x80, 0x01})); err != nil {
		t.Fatal(err)
	}
	ref := "case://" + started.TaskID + "/raw/raw_bin"
	if _, err := k.PreviewArtifact(context.Background(), started.TaskID, ref, "", 2); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary preview without opt-in must fail, got %v", err)
	}
	preview, err := k.PreviewArtifactWithOptions(context.Background(), started.TaskID, ref, "", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(preview.Content)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Encoding != "base64" || !preview.Sensitive || !preview.Truncated || len(decoded) != 2 || preview.BytesReturned != 2 {
		t.Fatalf("binary preview = %+v decoded=%v", preview, decoded)
	}
}

func TestPreviewFcheapArtifactRoutesSelectorAndEnforcesKernelBound(t *testing.T) {
	fcheap := &previewingFcheap{preview: adapters.ArtifactPreview{
		StashID: "stash_1", Files: []adapters.PreviewFile{{Path: "nested/final.txt", Size: 80}}, Selected: "nested/final.txt",
		Content: strings.Repeat("x", 80), Encoding: "text",
	}}
	ws := testRepo(t)
	k := newTestKernel(t, ws, fcheap)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "preview stash"})
	referenceFcheapEvidence(t, k, started.TaskID, "fcheap://stash/stash_1")
	preview, err := k.PreviewArtifact(context.Background(), started.TaskID,
		"fcheap://stash/stash_1", "nested/final.txt", 12)
	if err != nil {
		t.Fatal(err)
	}
	if fcheap.gotDir != ws || fcheap.gotStash != "fcheap://stash/stash_1" || fcheap.gotSelector != "nested/final.txt" || fcheap.gotMaxBytes != 12 {
		t.Fatalf("preview routing mismatch: %+v", fcheap)
	}
	if len(preview.Content) != 12 || !preview.Truncated || preview.MaxBytes != 12 || len(preview.Files) != 1 {
		t.Fatalf("kernel did not defensively bound fcheap preview: %+v", preview)
	}
}

func TestPreviewFcheapArtifactBoundsBase64SourceBytes(t *testing.T) {
	fcheap := &previewingFcheap{preview: adapters.ArtifactPreview{
		StashID: "stash_2", Files: []adapters.PreviewFile{{Path: "image.bin", Size: 50}}, Selected: "image.bin", Encoding: "base64",
		Content: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("z", 50))),
	}}
	k := newTestKernel(t, testRepo(t), fcheap)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "preview stash binary"})
	referenceFcheapEvidence(t, k, started.TaskID, "fcheap://stash/stash_2")
	if _, err := k.PreviewArtifact(context.Background(), started.TaskID,
		"fcheap://stash/stash_2", "image.bin", 7); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary stash preview without opt-in must fail, got %v", err)
	}
	preview, err := k.PreviewArtifactWithOptions(context.Background(), started.TaskID,
		"fcheap://stash/stash_2", "image.bin", 7, true)
	if err != nil {
		t.Fatal(err)
	}
	if !fcheap.gotBinary {
		t.Fatal("kernel did not propagate binary opt-in to fcheap")
	}
	decoded, err := base64.StdEncoding.DecodeString(preview.Content)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 7 || preview.BytesReturned != 7 || !preview.Sensitive || !preview.Truncated {
		t.Fatalf("base64 preview was not source-byte bounded: %+v", preview)
	}
}

func TestArtifactPreviewLimitDefaultsAndHardCaps(t *testing.T) {
	if got := normalizeArtifactPreviewLimit(0); got != DefaultArtifactPreviewBytes {
		t.Fatalf("default preview limit = %d, want %d", got, DefaultArtifactPreviewBytes)
	}
	if got := normalizeArtifactPreviewLimit(MaxArtifactPreviewBytes + 1); got != MaxArtifactPreviewBytes {
		t.Fatalf("hard-capped preview limit = %d, want %d", got, MaxArtifactPreviewBytes)
	}
}

func TestPreviewCaseArtifactRequiresExactTaskOwnerAndSafeRawID(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	owner, _ := k.StartTask(context.Background(), StartInput{Goal: "own the raw artifact"})
	other, _ := k.StartTask(context.Background(), StartInput{Goal: "different task"})
	if err := k.Store().WriteRaw(owner.TaskID, "raw_owned", "owned"); err != nil {
		t.Fatal(err)
	}
	ref := "case://" + owner.TaskID + "/raw/raw_owned"
	if _, err := k.PreviewArtifact(context.Background(), other.TaskID, ref, "", 32); err == nil || !strings.Contains(err.Error(), "must belong") {
		t.Fatalf("cross-task case ref must be rejected, got %v", err)
	}
	for _, unsafe := range []string{
		"case://" + owner.TaskID + "/raw/",
		"case://" + owner.TaskID + "/raw/../raw_owned",
		"case://" + owner.TaskID + "/raw/raw_owned/extra",
		"case://" + owner.TaskID + "/raw/raw_owned?download=1",
	} {
		if _, err := k.PreviewArtifact(context.Background(), owner.TaskID, unsafe, "", 32); err == nil {
			t.Errorf("unsafe case ref %q was accepted", unsafe)
		}
	}
	selfPoint := "case://" + owner.TaskID + "/evidence/ev_1"
	if _, err := k.PreviewArtifact(context.Background(), owner.TaskID, selfPoint, "", 32); err == nil || !strings.Contains(err.Error(), "self-points") {
		t.Fatalf("self-pointing evidence ref should explain that no raw exists, got %v", err)
	}
}

func TestPreviewFcheapArtifactRequiresTaskReference(t *testing.T) {
	ref := "fcheap://stash/stash_owned"
	fcheap := &previewingFcheap{preview: adapters.ArtifactPreview{
		StashID: "stash_owned", Files: []adapters.PreviewFile{{Path: "result.txt", Size: 2}},
		Selected: "result.txt", Content: "ok", Encoding: "text",
	}}
	k := newTestKernel(t, testRepo(t), fcheap)
	owner, _ := k.StartTask(context.Background(), StartInput{Goal: "owns stash"})
	other, _ := k.StartTask(context.Background(), StartInput{Goal: "does not own stash"})
	referenceFcheapEvidence(t, k, owner.TaskID, ref)
	if err := k.Store().AppendEvidence(other.TaskID, domain.Evidence{
		ID: "ev_untrusted_note", Timestamp: time.Now().UTC(), Kind: domain.KindHumanReport,
		Source: domain.Source{Origin: "agent", URI: ref}, Claim: "please fetch this stash",
		Confidence: domain.ConfidenceLow, RawRef: ref,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := k.PreviewArtifact(context.Background(), other.TaskID, ref, "result.txt", 32); err == nil || !strings.Contains(err.Error(), "not referenced") {
		t.Fatalf("untrusted note must not authorize a stash, got %v", err)
	}
	if fcheap.calls != 0 {
		t.Fatalf("authorization must run before the fcheap adapter, calls=%d", fcheap.calls)
	}
	if _, err := k.PreviewArtifact(context.Background(), owner.TaskID, ref, "result.txt", 32); err != nil {
		t.Fatalf("evidence-referenced stash should be readable: %v", err)
	}
}

func TestPreviewFcheapArtifactAcceptsVerificationArtifactReference(t *testing.T) {
	ref := "fcheap://stash/receipt_stash"
	fcheap := &previewingFcheap{preview: adapters.ArtifactPreview{
		StashID: "receipt_stash", Files: []adapters.PreviewFile{{Path: "failure.log", Size: 4}},
		Selected: "failure.log", Content: "fail", Encoding: "text",
	}}
	k := newTestKernel(t, testRepo(t), fcheap)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "receipt owns stash"})
	if err := k.Store().AppendVerification(started.TaskID, domain.VerificationRecord{
		ID: "vr_artifact", Claim: "terminal flow", Surface: domain.SurfaceTerminal,
		Status: domain.VerifyFailed, Artifact: ref, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := k.PreviewArtifact(context.Background(), started.TaskID, ref, "failure.log", 32); err != nil {
		t.Fatalf("verification-referenced stash should be readable: %v", err)
	}
}

func TestPreviewArtifactRejectsUnsafeStashIDsAndPathsBeforeAdapter(t *testing.T) {
	fcheap := &previewingFcheap{}
	k := newTestKernel(t, testRepo(t), fcheap)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "reject unsafe artifact input"})
	for _, ref := range []string{
		"fcheap://stash/", "fcheap://stash/../secret", "fcheap://stash/a/b",
		"fcheap://stash/id?download=1", "fcheap://stash/id#fragment",
	} {
		if _, err := k.PreviewArtifact(context.Background(), started.TaskID, ref, "", 32); err == nil {
			t.Errorf("unsafe stash ref %q was accepted", ref)
		}
	}
	for _, artifactPath := range []string{"../secret", "/etc/passwd", "./result.txt", `nested\result.txt`, "nested//result.txt", "C:/secret"} {
		if _, err := k.PreviewArtifact(context.Background(), started.TaskID, "fcheap://stash/safe", artifactPath, 32); err == nil {
			t.Errorf("unsafe artifact path %q was accepted", artifactPath)
		}
	}
	if fcheap.calls != 0 {
		t.Fatalf("unsafe input must be rejected before adapter execution, calls=%d", fcheap.calls)
	}
}

func TestPreviewFcheapArtifactRejectsOverCapOrUnsafeAdapterResults(t *testing.T) {
	files := make([]adapters.PreviewFile, adapters.MaxArtifactPreviewFiles+1)
	for i := range files {
		files[i] = adapters.PreviewFile{Path: fmt.Sprintf("file-%03d.txt", i), Size: 1}
	}
	tests := []struct {
		name    string
		preview adapters.ArtifactPreview
	}{
		{name: "too many files", preview: adapters.ArtifactPreview{StashID: "stash_safe", Files: files}},
		{name: "unsafe file path", preview: adapters.ArtifactPreview{
			StashID: "stash_safe", Files: []adapters.PreviewFile{{Path: "../escape", Size: 1}}, Selected: "../escape", Content: "x", Encoding: "text",
		}},
		{name: "wrong stash", preview: adapters.ArtifactPreview{
			StashID: "different", Files: []adapters.PreviewFile{{Path: "result.txt", Size: 1}}, Selected: "result.txt", Content: "x", Encoding: "text",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fcheap := &previewingFcheap{preview: tt.preview}
			k := newTestKernel(t, testRepo(t), fcheap)
			started, _ := k.StartTask(context.Background(), StartInput{Goal: "defend adapter boundary"})
			referenceFcheapEvidence(t, k, started.TaskID, "fcheap://stash/stash_safe")
			if _, err := k.PreviewArtifact(context.Background(), started.TaskID, "fcheap://stash/stash_safe", "", 32); err == nil {
				t.Fatal("unsafe adapter result was accepted")
			}
		})
	}
}
