package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestWorkspaceManifestPagesHistoryBeyondTenThousandRevisions(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	root := t.TempDir()
	if _, err := store.Create(ctx, "shared", root, testWorkspace()); err != nil {
		t.Fatal(err)
	}
	workspace, err := store.LoadByName(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO revisions(id,workspace_id,epoch,kind,snapshot,conflicts) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for index := range maxRevisionManifestPage + 1 {
		id := fmt.Sprintf("%064x", index+1)
		if _, err = statement.ExecContext(ctx, id, workspace.WorkspaceID, 1, "mutation", []byte(`{}`), []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
	}
	if err = statement.Close(); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var revisions []RevisionInventory
	after := ""
	for {
		page, pageErr := store.workspaceManifestPage(ctx, workspace, false, after, maxRevisionManifestPage)
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		if len(page.Revisions) > maxRevisionManifestPage {
			t.Fatalf("manifest page has %d revisions", len(page.Revisions))
		}
		revisions = append(revisions, page.Revisions...)
		if page.Next == "" {
			break
		}
		after = page.Next
	}
	if len(revisions) <= maxRevisionManifestPage {
		t.Fatalf("paged revisions=%d", len(revisions))
	}
}

func TestRevisionManifestImportRejectsMalformedAndOutOfOrderPages(t *testing.T) {
	ctx := context.Background()
	source, target := pairedRegistryStores(t)
	if _, err := source.Create(ctx, "shared", t.TempDir(), testWorkspace()); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{source.identity.ID(): WorkspaceAdmin}}
	if _, err := source.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	first, err := source.ManifestPageForLimit(ctx, "shared", target.identity.ID(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	importID, err := target.BeginAttachImportPage(ctx, "shared", t.TempDir(), source.identity.ID(), first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.ManifestPageForLimit(ctx, "shared", target.identity.ID(), first.Next, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = target.AppendRevisionImportManifest(ctx, importID, source.identity.ID(), first.WorkspaceID, RevisionImportAttach, strings.Repeat("f", 64), second); err == nil || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("out-of-order page error = %v", err)
	}
	malformed := second
	malformed.Epoch++
	if err = target.AppendRevisionImportManifest(ctx, importID, source.identity.ID(), first.WorkspaceID, RevisionImportAttach, first.Next, malformed); err == nil || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("inconsistent page error = %v", err)
	}
	duplicate := second
	duplicate.Revisions = append([]RevisionInventory(nil), second.Revisions...)
	duplicate.Revisions[0] = first.Revisions[0]
	if err = target.AppendRevisionImportManifest(ctx, importID, source.identity.ID(), first.WorkspaceID, RevisionImportAttach, first.Next, duplicate); err == nil {
		t.Fatal("duplicate page was accepted")
	}
	if err = target.AppendRevisionImportManifest(ctx, importID, source.identity.ID(), first.WorkspaceID, RevisionImportAttach, first.Next, second); err != nil {
		t.Fatal(err)
	}
	plan, err := target.FinishRevisionImportManifest(ctx, importID, source.identity.ID(), first.WorkspaceID, RevisionImportAttach)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := source.ManifestFor(ctx, "shared", target.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	wantHash, err := revisionManifestHash(complete)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ManifestHash != wantHash {
		t.Fatalf("manifest hash = %s, want %s", plan.ManifestHash, wantHash)
	}
}

func TestRevisionManifestImportRejectsExpiredAppend(t *testing.T) {
	ctx := context.Background()
	source, target := pairedRegistryStores(t)
	if _, err := source.Create(ctx, "shared", t.TempDir(), testWorkspace()); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{source.identity.ID(): WorkspaceAdmin}}
	if _, err := source.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	first, err := source.ManifestPageForLimit(ctx, "shared", target.identity.ID(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	importID, err := target.BeginAttachImportPage(ctx, "shared", t.TempDir(), source.identity.ID(), first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.ManifestPageForLimit(ctx, "shared", target.identity.ID(), first.Next, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.db.ExecContext(ctx, `UPDATE workspace_imports SET expires_at=0 WHERE id=?`, importID); err != nil {
		t.Fatal(err)
	}
	if err = target.AppendRevisionImportManifest(ctx, importID, source.identity.ID(), first.WorkspaceID, RevisionImportAttach, first.Next, second); !errors.Is(err, errRevisionImportExpired) {
		t.Fatalf("expired append error = %v", err)
	}
}

func TestIncompleteAndAbortedRevisionImportLeavesLiveHistoryUntouched(t *testing.T) {
	ctx := context.Background()
	source, target := pairedRegistryStores(t)
	root := t.TempDir()
	created, err := source.Create(ctx, "shared", t.TempDir(), testWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{source.identity.ID(): WorkspaceAdmin}}
	if _, err = source.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	manifest, err := source.ManifestFor(ctx, "shared", target.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := target.BeginAttachImport(ctx, "shared", root, source.identity.ID(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := source.RevisionsFor(ctx, "shared", target.identity.ID(), plan.Missing[:1])
	if err != nil {
		t.Fatal(err)
	}
	if err = target.StageRevisionImport(ctx, plan.ID, source.identity.ID(), created.WorkspaceID, RevisionImportAttach, plan.ManifestHash, revisions); err != nil {
		t.Fatal(err)
	}
	if _, err = target.FinishAttachImport(ctx, plan.ID, source.identity.ID(), created.WorkspaceID, plan.ManifestHash); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete import error = %v", err)
	}
	if _, err = target.LoadByName(ctx, "shared"); err == nil {
		t.Fatal("incomplete import exposed a workspace")
	}
	var live int
	if err = target.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions WHERE workspace_id=?`, created.WorkspaceID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("incomplete import published %d revisions", live)
	}
	if err = target.AbortRevisionImport(ctx, plan.ID, source.identity.ID(), created.WorkspaceID, RevisionImportAttach, plan.ManifestHash); err != nil {
		t.Fatal(err)
	}
	var staged int
	if err = target.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_import_revisions WHERE import_id=?`, plan.ID).Scan(&staged); err != nil {
		t.Fatal(err)
	}
	if staged != 0 {
		t.Fatalf("aborted import retained %d staged revisions", staged)
	}
}

func TestIncompleteSyncImportLeavesAttachedWorkspaceUntouched(t *testing.T) {
	ctx := context.Background()
	source, target := pairedRegistryStores(t)
	sourceRoot, targetRoot := t.TempDir(), t.TempDir()
	if _, err := source.Create(ctx, "shared", sourceRoot, testWorkspace()); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{source.identity.ID(): WorkspaceAdmin}}
	if _, err := source.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.ExportFor(ctx, "shared", target.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	attached, err := target.AttachFrom(ctx, "shared", targetRoot, bundle, source.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := source.Mutate(ctx, sourceRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["remote"] = "new"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := source.ManifestFor(ctx, "shared", target.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := target.BeginSyncImport(ctx, "shared", source.identity.ID(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Missing) != 1 || plan.Missing[0] != updated.Head {
		t.Fatalf("missing revisions = %v", plan.Missing)
	}
	if _, _, _, err = target.FinishSyncImport(ctx, plan.ID, source.identity.ID(), attached.WorkspaceID, plan.ManifestHash); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete sync error = %v", err)
	}
	unchanged, err := target.LoadByName(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Head != attached.Head || unchanged.State.Aliases["remote"] != "" {
		t.Fatalf("workspace changed after incomplete sync = %#v", unchanged)
	}
	var published int
	if err = target.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions WHERE id=?`, updated.Head).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if published != 0 {
		t.Fatal("incomplete sync published its missing revision")
	}
}

func TestStaleSyncImportQuarantinesWithoutPublishingRevisions(t *testing.T) {
	ctx := context.Background()
	left, right := pairedRegistryStores(t)
	leftRoot, rightRoot := t.TempDir(), t.TempDir()
	if _, err := left.Create(ctx, "shared", leftRoot, testWorkspace()); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{left.identity.ID(): WorkspaceAdmin}}
	if _, err := left.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	bundle, err := left.ExportFor(ctx, "shared", right.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.AttachFrom(ctx, "shared", rightRoot, bundle, left.identity.ID()); err != nil {
		t.Fatal(err)
	}
	stale, err := right.Mutate(ctx, rightRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["stale"] = "right"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	demoted := AccessPolicy{Mode: AccessSelected, Roles: map[string]string{left.identity.ID(): WorkspaceAdmin, right.identity.ID(): WorkspaceReplica}}
	authoritative, err := left.SetAccess(ctx, "shared", demoted)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := right.ManifestFor(ctx, "shared", left.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := left.BeginSyncImport(ctx, "shared", right.identity.ID(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := right.RevisionsFor(ctx, "shared", left.identity.ID(), plan.Missing)
	if err != nil {
		t.Fatal(err)
	}
	if err = left.StageRevisionImport(ctx, plan.ID, right.identity.ID(), authoritative.WorkspaceID, RevisionImportSync, plan.ManifestHash, revisions); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = left.FinishSyncImport(ctx, plan.ID, right.identity.ID(), authoritative.WorkspaceID, plan.ManifestHash); !errors.Is(err, ErrWorkspaceEpochStale) {
		t.Fatalf("stale import error = %v", err)
	}
	current, err := left.LoadByName(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if current.Head != authoritative.Head || current.Epoch != authoritative.Epoch {
		t.Fatalf("workspace after stale import = %#v", current)
	}
	var published, quarantined, imports int
	if err = left.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions WHERE id=?`, stale.Head).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if err = left.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_quarantine WHERE workspace_id=? AND source_device_id=? AND head_id=?`, authoritative.WorkspaceID, right.identity.ID(), stale.Head).Scan(&quarantined); err != nil {
		t.Fatal(err)
	}
	if err = left.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_imports WHERE id=?`, plan.ID).Scan(&imports); err != nil {
		t.Fatal(err)
	}
	if published != 0 || quarantined != 1 || imports != 0 {
		t.Fatalf("stale import published=%d quarantined=%d imports=%d", published, quarantined, imports)
	}
}

func TestRevisionImportRejectsUndeclaredDuplicateUnrequestedAndOversizedBatches(t *testing.T) {
	ctx := context.Background()
	source, target := pairedRegistryStores(t)
	created, err := source.Create(ctx, "shared", t.TempDir(), testWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{source.identity.ID(): WorkspaceAdmin}}
	if _, err = source.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	manifest, err := source.ManifestFor(ctx, "shared", target.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	malformedManifest := manifest
	malformedManifest.Revisions = append([]RevisionInventory(nil), manifest.Revisions...)
	malformedManifest.Revisions[0].ID = "bad"
	if _, err = target.BeginAttachImport(ctx, "malformed", t.TempDir(), source.identity.ID(), malformedManifest); err == nil || !strings.Contains(err.Error(), "inventory") {
		t.Fatalf("malformed manifest error = %v", err)
	}
	plan, err := target.BeginAttachImport(ctx, "shared", t.TempDir(), source.identity.ID(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := source.RevisionsFor(ctx, "shared", target.identity.ID(), plan.Missing[:1])
	if err != nil {
		t.Fatal(err)
	}
	undeclared := revisions[0]
	undeclared.ID = strings.Repeat("a", 64)
	if err = target.StageRevisionImport(ctx, plan.ID, source.identity.ID(), created.WorkspaceID, RevisionImportAttach, plan.ManifestHash, []Revision{undeclared}); err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("undeclared revision error = %v", err)
	}
	malformed := revisions[0]
	malformed.ID = "bad"
	if err = target.StageRevisionImport(ctx, plan.ID, source.identity.ID(), created.WorkspaceID, RevisionImportAttach, plan.ManifestHash, []Revision{malformed}); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("malformed revision error = %v", err)
	}
	if err = target.StageRevisionImport(ctx, plan.ID, source.identity.ID(), created.WorkspaceID, RevisionImportAttach, plan.ManifestHash, revisions); err != nil {
		t.Fatal(err)
	}
	if err = target.StageRevisionImport(ctx, plan.ID, source.identity.ID(), created.WorkspaceID, RevisionImportAttach, plan.ManifestHash, revisions); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate revision error = %v", err)
	}
	oversized := make([]Revision, maxImportBatchRevisions+1)
	if err = target.StageRevisionImport(ctx, plan.ID, source.identity.ID(), created.WorkspaceID, RevisionImportAttach, plan.ManifestHash, oversized); err == nil || !strings.Contains(err.Error(), "count") {
		t.Fatalf("oversized batch error = %v", err)
	}
	if err = target.AbortRevisionImport(ctx, plan.ID, source.identity.ID(), created.WorkspaceID, RevisionImportAttach, plan.ManifestHash); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.ExportFor(ctx, "shared", target.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.AttachFrom(ctx, "shared", t.TempDir(), bundle, source.identity.ID()); err != nil {
		t.Fatal(err)
	}
	syncPlan, err := target.BeginSyncImport(ctx, "shared", source.identity.ID(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(syncPlan.Missing) != 0 {
		t.Fatalf("existing history requested again = %v", syncPlan.Missing)
	}
	if err = target.StageRevisionImport(ctx, syncPlan.ID, source.identity.ID(), created.WorkspaceID, RevisionImportSync, syncPlan.ManifestHash, revisions); err == nil || !strings.Contains(err.Error(), "unrequested") {
		t.Fatalf("unrequested revision error = %v", err)
	}
}

func TestOpenPurgesExpiredRevisionImports(t *testing.T) {
	ctx := context.Background()
	source, target := pairedRegistryStores(t)
	created, err := source.Create(ctx, "shared", t.TempDir(), testWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{source.identity.ID(): WorkspaceAdmin}}
	if _, err = source.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	manifest, err := source.ManifestFor(ctx, "shared", target.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := target.BeginAttachImport(ctx, "shared", t.TempDir(), source.identity.ID(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.db.ExecContext(ctx, `UPDATE workspace_imports SET expires_at=0 WHERE id=?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	path := target.path
	if err = target.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	var imports int
	if err = reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_imports WHERE workspace_id=?`, created.WorkspaceID).Scan(&imports); err != nil {
		t.Fatal(err)
	}
	if imports != 0 {
		t.Fatalf("expired imports after Open = %d", imports)
	}
}
