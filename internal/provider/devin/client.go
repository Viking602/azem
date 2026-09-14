package devin

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	devinauth "github.com/Viking602/azem/internal/auth/devin"
	"github.com/Viking602/azem/internal/netproxy"
	"github.com/Viking602/azem/internal/provider/catalog"
	hyprovider "github.com/Viking602/venat/provider"
)

const (
	DefaultAPIURL = "https://server.codeium.com"
	cliVersion    = "3000.6.2"
	authPath      = "/exa.auth_pb.AuthService/GetUserJwt"
	catalogPath   = "/exa.api_server_pb.ApiServerService/GetCliModelConfigs"
	chatPath      = "/exa.api_server_pb.ApiServerService/GetChatMessage"
	assignPath    = "/exa.api_server_pb.ApiServerService/AssignModel"
	maxFrameBytes = 16 << 20
)

func rejectionError(status int, code, message string, secrets ...string) error {
	for _, secret := range secrets {
		for _, value := range []string{secret, strings.TrimPrefix(secret, devinauth.SessionTokenPrefix)} {
			if value != "" {
				message = strings.ReplaceAll(message, value, "[redacted]")
				code = strings.ReplaceAll(code, value, "[redacted]")
			}
		}
	}
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		message = "Devin stream was rejected"
	}
	message = string([]rune(message)[:min(512, len([]rune(message)))])
	failure := hyprovider.NewHTTPError("devin", status, message)
	failure.Code = string([]rune(code)[:min(64, len([]rune(code)))])
	return failure
}

func newHTTPClient() *http.Client {
	client := netproxy.NewHTTPClient(0)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport.ResponseHeaderTimeout = 10 * time.Minute
	}
	return client
}

func metadata(token, jwt string, discovery bool) proto {
	var p proto
	token = devinauth.SessionTokenPrefix + strings.TrimPrefix(strings.TrimSpace(token), devinauth.SessionTokenPrefix)
	name, version := "devin-cli", cliVersion
	if discovery {
		name, version = "chisel", "0.0.0-dev"
	}
	p.text(1, name)
	p.text(7, version)
	p.text(2, version)
	p.text(12, "chisel")
	p.text(3, token)
	p.text(4, "en")
	osName := runtime.GOOS
	if osName != "windows" && osName != "darwin" {
		osName = "linux"
	}
	p.text(5, osName)
	p.text(21, jwt)
	if discovery {
		// Native display slots; internal/quick-review configs are filtered below.
		for _, display := range []uint64{3, 4, 6, 7, 8} {
			p.number(30, display)
		}
	} else {
		p.text(28, "chisel")
	}
	return p
}

func unary(ctx context.Context, client *http.Client, endpoint string, payload proto) (*protoMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, hyprovider.NewHTTPError("devin", res.StatusCode, "Devin request was rejected")
	}
	raw, err := readBounded(res.Body)
	if err != nil {
		return nil, err
	}
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		raw, err = decompress(raw)
	}
	if err != nil {
		return nil, err
	}
	decoded := decodeProto(raw, nil)
	return decoded, decoded.err
}

func readBounded(reader io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxFrameBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxFrameBytes {
		return nil, fmt.Errorf("Devin response exceeds %d bytes", maxFrameBytes)
	}
	return raw, nil
}

func decompress(raw []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid Devin compressed response")
	}
	defer reader.Close()
	return readBounded(reader)
}

// Only authenticated Codeium/Devin API hosts can receive the session credential.
func apiServer(base, custom string) (string, error) {
	if custom == "" {
		return base, nil
	}
	u, err := url.Parse(custom)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Port() != "" && u.Port() != "443") {
		return "", fmt.Errorf("Devin returned an invalid API host")
	}
	host := strings.ToLower(u.Hostname())
	if host != "server.codeium.com" && !strings.HasSuffix(host, ".codeium.com") && host != "api.devin.ai" && !strings.HasSuffix(host, ".api.devin.ai") {
		return "", fmt.Errorf("Devin returned an untrusted API host")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// FetchModels obtains the authenticated CLI catalog, never a bundled fallback.
func FetchModels(ctx context.Context, token string) ([]catalog.Model, error) {
	return fetchModels(ctx, newHTTPClient(), DefaultAPIURL, token)
}

func fetchModels(ctx context.Context, client *http.Client, base, token string) ([]catalog.Model, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("Devin login is required")
	}
	var request proto
	request.data(1, metadata(token, "", true))
	res, err := unary(ctx, client, base+catalogPath, request)
	if err != nil {
		return nil, err
	}
	var models []catalog.Model
	for _, field := range res.values(1, 2) {
		row := decodeProto(field.data, res.budget)
		info := row.child(23)
		features := info.child(6)
		id, label := strings.TrimSpace(row.text(22)), strings.TrimSpace(row.text(1))
		disabled, display := row.number(4) != 0, info.number(22)
		model := catalog.Model{ID: id, Name: label, Description: row.text(27), ContextWindow: row.number(18), MaxOutputTokens: info.number(13), InputModalities: []string{"text"}, OutputModalities: []string{"text"}}
		images := row.number(5) != 0
		model.SupportsTools = true
		if len(info.data(6)) > 0 {
			images = features.number(11) != 0
			model.SupportsTools = features.number(12) != 0
			model.SupportsReasoning = features.number(15) != 0
			model.SupportsParallel = features.number(21) != 0
		}
		model.DevinRouter = display == 3 || info.number(25) != 0
		for _, parsed := range []*protoMessage{row, info, features} {
			if parsed.err != nil {
				return nil, parsed.err
			}
		}
		if id == "" || disabled || display == 4 || display == 6 {
			continue
		}
		if model.Name == "" {
			model.Name = id
		}
		// ponytail: unknown limits use CLI defaults; replace only with account metadata.
		if model.ContextWindow <= 0 {
			model.ContextWindow = 200000
		}
		if model.MaxOutputTokens <= 0 {
			model.MaxOutputTokens = 64000
		}
		if images {
			model.InputModalities = append(model.InputModalities, "image")
		}
		models = append(models, model)
	}
	if res.err != nil {
		return nil, res.err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("Devin returned no available CLI models; check account access or CLI compatibility")
	}
	return models, nil
}
