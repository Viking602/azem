package session

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// LoadReferenceBlocks reads only recent visible prose on the active branch.
// Tool payloads and provider checkpoints are never hydrated for a mention.
func (s *Service) LoadReferenceBlocks(ctx context.Context, sessionID string) ([]Block, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE branch(entry_id,parent_entry_id,block_sequence) AS (
			SELECT entry.entry_id,entry.parent_entry_id,entry.block_sequence
			FROM session_graph_entries entry JOIN session_graphs graph
			ON graph.session_id=entry.session_id AND graph.active_leaf_entry_id=entry.entry_id
			WHERE entry.session_id=?
			UNION ALL
			SELECT entry.entry_id,entry.parent_entry_id,entry.block_sequence
			FROM session_graph_entries entry JOIN branch ON entry.entry_id=branch.parent_entry_id
			WHERE entry.session_id=?
		)
		SELECT b.sequence,b.data,b.data_sha256 FROM session_blocks b
		JOIN branch ON branch.block_sequence=b.sequence
		WHERE b.session_id=? AND b.kind IN ('user','assistant')
		ORDER BY b.sequence DESC LIMIT 65`, sessionID, sessionID, sessionID)
	if err != nil {
		return nil, false, fmt.Errorf("load referenced conversation: %w", err)
	}
	defer rows.Close()
	var blocks []Block
	users, scanned := 0, 0
	truncated := false
	for rows.Next() {
		scanned++
		if scanned > 64 {
			truncated = true
			break
		}
		var sequence int64
		var data []byte
		var digest string
		if err := rows.Scan(&sequence, &data, &digest); err != nil {
			return nil, false, err
		}
		payload, err := s.decodeBlockJSON(ctx, data, digest)
		if err != nil {
			return nil, false, err
		}
		var block Block
		if err := json.Unmarshal(payload, &block); err != nil {
			return nil, false, err
		}
		if (block.AgentID != "" && block.AgentID != "main") || block.State == "hook" || block.State == "subagent_wake" {
			continue
		}
		if block.Kind == "user" {
			if users == 3 {
				break
			}
			users++
		}
		block.Sequence = sequence
		blocks = append(blocks, block)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	// The fourth user's answer precedes that user in descending order. Drop it.
	for len(blocks) > 0 && blocks[len(blocks)-1].Kind != "user" {
		blocks = blocks[:len(blocks)-1]
	}
	slices.Reverse(blocks)
	return blocks, truncated, nil
}
