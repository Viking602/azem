package agent

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	asarPrefixBytes = 16
	maxAsarHeader   = 16 << 20
)

type asarNode struct {
	Files    map[string]*asarNode `json:"files,omitempty"`
	Size     *int64               `json:"size,omitempty"`
	Offset   string               `json:"offset,omitempty"`
	Unpacked bool                 `json:"unpacked,omitempty"`
	Link     string               `json:"link,omitempty"`
}

func readAsarMember(path, member string, limit int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries, err := readAsarEntries(file, nil, "")
	if err != nil {
		return nil, err
	}
	payload, ok := entries[member]
	if !ok {
		return nil, os.ErrNotExist
	}
	if len(payload) > limit {
		return append([]byte(nil), payload[:limit]...), nil
	}
	return append([]byte(nil), payload...), nil
}

func rewriteAsar(ctx context.Context, root *os.Root, source string, output *os.File, target string, payload []byte, exists bool) error {
	entries := make(map[string][]byte)
	if exists {
		input, err := root.Open(source)
		if err != nil {
			return err
		}
		entries, err = readAsarEntries(input, root, source)
		closeErr := input.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	entries[target] = append([]byte(nil), payload...)
	if err := ctx.Err(); err != nil {
		return err
	}
	return encodeAsar(output, entries)
}

func readAsarEntries(file *os.File, root *os.Root, archivePath string) (map[string][]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < asarPrefixBytes {
		return nil, errors.New("invalid ASAR: truncated header")
	}
	prefix := make([]byte, asarPrefixBytes)
	if _, err := file.ReadAt(prefix, 0); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(prefix[0:4]) != 4 {
		return nil, errors.New("invalid ASAR: bad size pickle")
	}
	headerSize := int64(binary.LittleEndian.Uint32(prefix[4:8]))
	innerSize := int64(binary.LittleEndian.Uint32(prefix[8:12]))
	jsonSize := int64(binary.LittleEndian.Uint32(prefix[12:16]))
	alignedJSON := jsonSize + (4-jsonSize%4)%4
	if headerSize < 8 || headerSize > maxAsarHeader || headerSize != innerSize+4 || innerSize != alignedJSON+4 {
		return nil, errors.New("invalid ASAR: inconsistent header lengths")
	}
	dataOffset := int64(8) + headerSize
	if dataOffset > info.Size() || jsonSize <= 0 || jsonSize > headerSize-8 {
		return nil, errors.New("invalid ASAR: header exceeds archive")
	}
	headerJSON := make([]byte, jsonSize)
	if _, err := file.ReadAt(headerJSON, asarPrefixBytes); err != nil {
		return nil, err
	}
	var header asarNode
	if err := json.Unmarshal(headerJSON, &header); err != nil || header.Files == nil {
		return nil, errors.New("invalid ASAR: root files object is missing")
	}
	entries := make(map[string][]byte)
	count := 0
	var total int64
	var walk func(map[string]*asarNode, string) error
	walk = func(nodes map[string]*asarNode, parent string) error {
		names := make([]string, 0, len(nodes))
		for name := range nodes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
				return fmt.Errorf("invalid ASAR member name %q", name)
			}
			node := nodes[name]
			if node == nil {
				return errors.New("invalid ASAR: null member")
			}
			member := name
			if parent != "" {
				member = parent + "/" + name
			}
			if node.Files != nil {
				if node.Size != nil || node.Link != "" {
					return fmt.Errorf("invalid ASAR directory %q", member)
				}
				if err := walk(node.Files, member); err != nil {
					return err
				}
				continue
			}
			if node.Link != "" {
				return fmt.Errorf("ASAR link %q is unsupported for rewrite", member)
			}
			if node.Size == nil || *node.Size < 0 {
				return fmt.Errorf("invalid ASAR file %q", member)
			}
			count++
			if count > maxArchiveEntries {
				return errors.New("ASAR contains too many entries")
			}
			total += *node.Size
			if total > maxArchiveRewriteSize {
				return errors.New("ASAR expands beyond rewrite limit")
			}
			var payload []byte
			if node.Unpacked {
				if root == nil || archivePath == "" {
					return fmt.Errorf("ASAR member %q is unpacked", member)
				}
				payload, err = root.ReadFile(archivePath + ".unpacked/" + filepath.FromSlash(member))
				if err != nil {
					return err
				}
				if int64(len(payload)) != *node.Size {
					return fmt.Errorf("ASAR unpacked member %q changed size", member)
				}
			} else {
				offset, parseErr := strconv.ParseInt(node.Offset, 10, 64)
				if parseErr != nil || offset < 0 || offset > info.Size()-dataOffset || *node.Size > info.Size()-dataOffset-offset {
					return fmt.Errorf("invalid ASAR offset for %q", member)
				}
				payload = make([]byte, *node.Size)
				if _, err := file.ReadAt(payload, dataOffset+offset); err != nil {
					return err
				}
			}
			entries[member] = payload
		}
		return nil
	}
	if err := walk(header.Files, ""); err != nil {
		return nil, err
	}
	return entries, nil
}

func encodeAsar(output *os.File, entries map[string][]byte) error {
	root := &asarNode{Files: make(map[string]*asarNode)}
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var offset int64
	for _, member := range paths {
		normalized, err := normalizeArchiveMember(member)
		if err != nil || normalized != member {
			return fmt.Errorf("invalid ASAR member %q", member)
		}
		parts := strings.Split(member, "/")
		directory := root
		for _, part := range parts[:len(parts)-1] {
			node := directory.Files[part]
			if node == nil {
				node = &asarNode{Files: make(map[string]*asarNode)}
				directory.Files[part] = node
			}
			if node.Files == nil {
				return fmt.Errorf("ASAR path %q crosses a file", member)
			}
			directory = node
		}
		name := parts[len(parts)-1]
		if directory.Files[name] != nil {
			return fmt.Errorf("duplicate ASAR member %q", member)
		}
		size := int64(len(entries[member]))
		directory.Files[name] = &asarNode{Size: &size, Offset: strconv.FormatInt(offset, 10)}
		offset += size
		if offset > maxArchiveRewriteSize {
			return errors.New("ASAR payload exceeds rewrite limit")
		}
	}
	headerJSON, err := json.Marshal(root)
	if err != nil {
		return err
	}
	if len(headerJSON) > maxAsarHeader {
		return errors.New("ASAR header exceeds limit")
	}
	padded := len(headerJSON) + (4-len(headerJSON)%4)%4
	innerSize := 4 + padded
	headerSize := 4 + innerSize
	prefix := make([]byte, asarPrefixBytes)
	binary.LittleEndian.PutUint32(prefix[0:4], 4)
	binary.LittleEndian.PutUint32(prefix[4:8], uint32(headerSize))
	binary.LittleEndian.PutUint32(prefix[8:12], uint32(innerSize))
	binary.LittleEndian.PutUint32(prefix[12:16], uint32(len(headerJSON)))
	if _, err := output.Write(prefix); err != nil {
		return err
	}
	if _, err := output.Write(headerJSON); err != nil {
		return err
	}
	if padding := padded - len(headerJSON); padding > 0 {
		if _, err := output.Write(make([]byte, padding)); err != nil {
			return err
		}
	}
	for _, member := range paths {
		if _, err := output.Write(entries[member]); err != nil {
			return err
		}
	}
	if err := output.Sync(); err != nil {
		return err
	}
	return output.Close()
}
