package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Viking602/venat/tool"
)

type recordingFileBroker struct {
	writes  []brokeredWrite
	deletes []string
}

type brokeredWrite struct {
	destination string
	content     string
}

func (broker *recordingFileBroker) BrokerWrite(_ context.Context, destination string, content []byte, _ error, _ string) (bool, error) {
	broker.writes = append(broker.writes, brokeredWrite{destination: destination, content: string(content)})
	return true, nil
}

func (broker *recordingFileBroker) BrokerDelete(_ context.Context, destination string, _ error, _ string, confirmedFile bool) (bool, error) {
	if !confirmedFile {
		return false, nil
	}
	broker.deletes = append(broker.deletes, destination)
	return true, nil
}

func TestWriteUsesExtensionBrokerOnlyAfterPermissionFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission fixture requires a non-root Unix process")
	}
	workspace := t.TempDir()
	locked := filepath.Join(workspace, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	broker := &recordingFileBroker{}
	ref := &fileMutationBrokerRef{}
	ref.set(broker)
	driver := newWriteDriver(workspace, nil, nil, ref)
	arguments, _ := json.Marshal(map[string]any{"path": "locked/brokered.txt", "content": "brokered"})
	result, err := driver.Execute(context.Background(), tool.Call{ID: "write", Name: ToolWriteFile, Arguments: arguments}, nil)
	expectedDestination, resolveErr := brokerDestination(workspace, "locked/brokered.txt", true)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err != nil || result.IsError || len(broker.writes) != 1 || broker.writes[0].content != "brokered" || broker.writes[0].destination != expectedDestination {
		t.Fatalf("brokered write result=%#v error=%v calls=%#v", result, err, broker.writes)
	}

	arguments, _ = json.Marshal(map[string]any{"path": "locked", "content": "not a directory replacement"})
	result, err = driver.Execute(context.Background(), tool.Call{ID: "directory", Name: ToolWriteFile, Arguments: arguments}, nil)
	if err != nil || !result.IsError || len(broker.writes) != 1 {
		t.Fatalf("non-permission failure reached broker: result=%#v error=%v calls=%#v", result, err, broker.writes)
	}
}

func TestHashlineCommitBrokersWritesAndDeletesAfterPermissionFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission fixture requires a non-root Unix process")
	}
	workspace := t.TempDir()
	locked := filepath.Join(workspace, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(locked, "file.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	broker := &recordingFileBroker{}
	ref := &fileMutationBrokerRef{}
	ref.set(broker)
	driver := &hashlineDriver{root: workspace, clipboard: newHashlineClipboard(), broker: ref}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	prepared := []*hashlinePreparedFile{{source: "locked/file.txt", destination: "locked/file.txt", rawOriginal: []byte("old\n"), final: "new\n", mode: 0o600}}
	if err := driver.commitPatch(context.Background(), root, prepared); err != nil || len(broker.writes) != 1 || broker.writes[0].content != "new\n" {
		t.Fatalf("brokered edit error=%v writes=%#v", err, broker.writes)
	}
	broker.writes = nil
	prepared = []*hashlinePreparedFile{{source: "locked/file.txt", destination: "locked/file.txt", rawOriginal: []byte("old\n"), mode: 0o600, remove: true}}
	expectedDestination, resolveErr := brokerDestination(workspace, "locked/file.txt", false)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err := driver.commitPatch(context.Background(), root, prepared); err != nil || len(broker.deletes) != 1 || broker.deletes[0] != expectedDestination {
		t.Fatalf("brokered delete error=%v deletes=%#v", err, broker.deletes)
	}
}

func TestBrokerDestinationRejectsSymlinkEscapeAndUnknownParent(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := brokerDestination(workspace, "link/file.txt", true); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("symlink escape error = %v", err)
	}
	expectedRoot, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := brokerDestination(workspace, "missing/nested/file.txt", true)
	if err != nil || destination != filepath.Join(expectedRoot, "missing", "nested", "file.txt") {
		t.Fatalf("missing-parent destination=%q error=%v", destination, err)
	}
}
