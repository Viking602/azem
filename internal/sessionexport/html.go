package sessionexport

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"time"

	"github.com/Viking602/azem/internal/session"
)

type htmlAttachment struct {
	Name, MIME string
	Size       int64
}

type htmlBlock struct {
	Sequence    int64
	Heading     string
	Kind        string
	Content     string
	Label       string
	Attachments []htmlAttachment
}

type htmlTool struct {
	Name, State, Arguments, Content string
}

type htmlDocument struct {
	Title, SessionID, Workspace, Branch, Source string
	CreatedAt, UpdatedAt                        string
	AllBranches                                 bool
	Blocks                                      []htmlBlock
	Tools                                       []htmlTool
}

func writeHTML(ctx context.Context, output io.Writer, snapshot session.ExportSnapshot, options Options) error {
	labels := treeLabels(snapshot.Tree.Roots)
	blocks := exportedBlocks(snapshot, options)
	view := htmlDocument{
		Title: snapshot.Session.Title, SessionID: snapshot.Session.ID, Workspace: snapshot.Session.Workspace,
		Branch: snapshot.Tree.ActiveBranch, Source: snapshot.Tree.SourceKind,
		CreatedAt: formatTime(snapshot.Session.CreatedAt), UpdatedAt: formatTime(snapshot.Session.UpdatedAt),
		AllBranches: options.AllBranches, Blocks: make([]htmlBlock, 0, len(blocks)),
	}
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := htmlBlock{Sequence: block.Sequence, Heading: blockHeading(block), Kind: block.Kind, Content: block.Content, Label: labels[block.Sequence]}
		for _, attachment := range block.Attachments {
			item.Attachments = append(item.Attachments, htmlAttachment{
				Name: firstNonempty(attachment.Name, attachment.ID, "unnamed"), MIME: firstNonempty(attachment.MIME, "unknown"), Size: attachment.Size,
			})
		}
		view.Blocks = append(view.Blocks, item)
	}
	for _, record := range exportedTools(snapshot, options) {
		if err := ctx.Err(); err != nil {
			return err
		}
		view.Tools = append(view.Tools, htmlTool{Name: record.Name, State: record.State, Arguments: string(record.Arguments), Content: record.Content})
	}
	templateValue, err := template.New("session").Parse(sessionHTMLTemplate)
	if err != nil {
		return fmt.Errorf("parse session export template: %w", err)
	}
	if err := templateValue.Execute(output, view); err != nil {
		return fmt.Errorf("render session export: %w", err)
	}
	return nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

const sessionHTMLTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">
<title>{{.Title}} · Azem session</title>
<style>
:root{color-scheme:light dark;--paper:#f5f4ef;--ink:#171714;--muted:#6a6860;--line:#d8d5ca;--panel:#fff;--accent:#315ddc}*{box-sizing:border-box}body{margin:0;background:var(--paper);color:var(--ink);font:15px/1.6 ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.shell{width:min(900px,calc(100% - 32px));margin:48px auto 96px}.header{border-top:3px solid var(--ink);border-bottom:1px solid var(--line);padding:24px 0 20px}.eyebrow{margin:0 0 8px;color:var(--muted);font:600 11px/1.2 ui-monospace,SFMono-Regular,monospace;letter-spacing:.12em;text-transform:uppercase}h1{font-size:clamp(32px,7vw,68px);line-height:.96;letter-spacing:-.045em;margin:0 0 24px}.meta{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:8px 24px;color:var(--muted);font-size:12px}.meta b{display:block;color:var(--ink);font-weight:600}.transcript{margin-top:36px}.entry{display:grid;grid-template-columns:128px minmax(0,1fr);gap:24px;padding:24px 0;border-bottom:1px solid var(--line)}.role{font:600 11px/1.4 ui-monospace,SFMono-Regular,monospace;text-transform:uppercase;letter-spacing:.08em}.label{display:block;margin-top:6px;color:var(--accent);text-transform:none;letter-spacing:0}.content{white-space:pre-wrap;overflow-wrap:anywhere}.attachment{display:inline-block;margin:12px 8px 0 0;padding:4px 8px;border:1px solid var(--line);font-size:12px;color:var(--muted)}.tools{margin-top:48px}.tool{margin:0 0 12px;border:1px solid var(--line);background:var(--panel)}.tool summary{cursor:pointer;padding:12px 16px;font-weight:600}.tool pre{margin:0;padding:16px;border-top:1px solid var(--line);white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.55 ui-monospace,SFMono-Regular,monospace}.state{color:var(--muted);font-weight:400}@media(max-width:640px){.shell{margin-top:24px}.entry{grid-template-columns:1fr;gap:8px}}@media(prefers-color-scheme:dark){:root{--paper:#171816;--ink:#f3f1e8;--muted:#a5a299;--line:#383a35;--panel:#20211e;--accent:#8dacff}}
</style>
</head>
<body><main class="shell">
<header class="header"><p class="eyebrow">Azem session export{{if .AllBranches}} · all branches{{end}}</p><h1>{{.Title}}</h1><div class="meta"><span><b>Session</b>{{.SessionID}}</span><span><b>Branch</b>{{.Branch}}</span>{{if .Workspace}}<span><b>Workspace</b>{{.Workspace}}</span>{{end}}<span><b>Created</b>{{.CreatedAt}}</span><span><b>Updated</b>{{.UpdatedAt}}</span>{{if and .Source (ne .Source "native")}}<span><b>Source</b>{{.Source}}</span>{{end}}</div></header>
<section class="transcript" aria-label="Conversation">{{range .Blocks}}<article class="entry" data-kind="{{.Kind}}"><div class="role">{{.Heading}}{{if .Label}}<span class="label">{{.Label}}</span>{{end}}</div><div><div class="content">{{.Content}}</div>{{range .Attachments}}<span class="attachment">{{.Name}} · {{.MIME}} · {{.Size}} bytes</span>{{end}}</div></article>{{else}}<p>No transcript entries.</p>{{end}}</section>
{{if .Tools}}<section class="tools" aria-label="Tool activity"><p class="eyebrow">Tool activity</p>{{range .Tools}}<details class="tool"><summary>{{.Name}} <span class="state">· {{.State}}</span></summary>{{if .Arguments}}<pre>{{.Arguments}}</pre>{{end}}{{if .Content}}<pre>{{.Content}}</pre>{{end}}</details>{{end}}</section>{{end}}
</main></body></html>`
