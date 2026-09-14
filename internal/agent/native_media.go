package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/netproxy"
	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

const ToolGenerateImage = "generate_image"
const ToolTTS = "tts"
const ToolInspectImage = "inspect_image"

type mediaDriver struct {
	root, operation, networkPolicy string
	client                         *http.Client
	endpoint, apiKey               string
}

func (d *mediaDriver) Definition() tool.Definition {
	additional := false
	definition := tool.Definition{Name: d.operation, Concurrency: tool.ConcurrencyParallel}
	switch d.operation {
	case ToolTTS:
		definition.Description = "Synthesize speech to a new workspace file using macOS say (AIFF) or Linux espeak-ng/espeak (WAV). No cloud account or JS/TS runtime. output_path must have the matching extension; existing files are never overwritten."
		definition.InputSchema = tool.Schema{Type: "object", Required: []string{"text", "output_path"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"text": {Type: "string"}, "voice_id": {Type: "string"}, "output_path": {Type: "string"}}}
	case ToolGenerateImage:
		definition.Description = "Generate or edit an image through the OpenAI-compatible Images API. Requires OPENAI_API_KEY; optional AZEM_IMAGE_BASE_URL selects the trusted provider endpoint. Inputs are workspace image paths. Writes a new PNG, never overwrites an existing file. Credential values never enter tool output."
		definition.InputSchema = tool.Schema{Type: "object", Required: []string{"prompt", "output_path"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"prompt": {Type: "string"}, "model": {Type: "string"}, "size": {Type: "string", Enum: []string{"1024x1024", "1536x1024", "1024x1536", "auto"}}, "input": {Type: "array", Items: &tool.Schema{Type: "string"}}, "output_path": {Type: "string"}}}
	default:
		definition.Description = "Inspect a workspace image, optionally crop it with [x,y,width,height]. Returns actual image content for model vision. Files, dimensions and decoded pixels are bounded; no external runtime is needed."
		definition.InputSchema = tool.Schema{Type: "object", Required: []string{"path"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"path": {Type: "string"}, "crop": {Type: "array", Items: &tool.Schema{Type: "integer"}}}}
	}
	return definition
}

func (d *mediaDriver) ToolPolicy() agentruntime.ToolPolicy {
	if d.operation == ToolInspectImage {
		return readOnlyPolicy("image", "read")
	}
	if d.operation == ToolTTS {
		return workspaceWritePolicy("audio", "write")
	}
	return approvedExternalPolicy("image", "network", "write")
}

func (d *mediaDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Text   string   `json:"text"`
		Voice  string   `json:"voice_id"`
		Prompt string   `json:"prompt"`
		Model  string   `json:"model"`
		Size   string   `json:"size"`
		Input  []string `json:"input"`
		Output string   `json:"output_path"`
		Path   string   `json:"path"`
		Crop   []int    `json:"crop"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return toolError(call, err.Error()), nil
	}
	if d.operation == ToolInspectImage {
		data, err := nativeImageFile(d.root, input.Path)
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		if len(input.Crop) != 0 {
			if len(input.Crop) != 4 || input.Crop[0] < 0 || input.Crop[1] < 0 || input.Crop[2] < 1 || input.Crop[3] < 1 {
				return toolError(call, "crop must be [x,y,width,height] with positive dimensions"), nil
			}
			decoded, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				return toolError(call, err.Error()), nil
			}
			x, y, w, h := input.Crop[0], input.Crop[1], input.Crop[2], input.Crop[3]
			if x > decoded.Bounds().Dx() || y > decoded.Bounds().Dy() || w > decoded.Bounds().Dx()-x || h > decoded.Bounds().Dy()-y {
				return toolError(call, "crop exceeds image bounds"), nil
			}
			cropped, ok := decoded.(interface {
				SubImage(image.Rectangle) image.Image
			})
			if !ok {
				return toolError(call, "image format cannot be cropped"), nil
			}
			var encoded bytes.Buffer
			if err := png.Encode(&encoded, cropped.SubImage(image.Rect(x, y, x+w, y+h))); err != nil {
				return toolError(call, err.Error()), nil
			}
			data = encoded.Bytes()
		}
		return nativeImageResult(call, data, input.Path)
	}
	if input.Output == "" {
		return toolError(call, "output_path is required"), nil
	}
	if _, err := workspaceRelativePath(d.root, input.Output); err != nil {
		return toolError(call, err.Error()), nil
	}
	if err := nativeArtifactAvailable(d.root, input.Output); err != nil {
		return toolError(call, err.Error()), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var data []byte
	var err error
	if d.operation == ToolTTS {
		if strings.TrimSpace(input.Text) == "" || len(input.Text) > 100000 || len(input.Voice) > 100 {
			return toolError(call, "text is required (max 100000 bytes); voice_id max 100 bytes"), nil
		}
		directory, err := os.MkdirTemp("", "azem-speech-")
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		defer os.RemoveAll(directory)
		var program string
		var arguments []string
		if runtime.GOOS == "darwin" {
			if strings.ToLower(filepath.Ext(input.Output)) != ".aiff" {
				return toolError(call, "macOS speech output_path must end in .aiff"), nil
			}
			program, arguments = "/usr/bin/say", []string{"-o", filepath.Join(directory, "speech.aiff"), "-f", "-"}
			if input.Voice != "" {
				arguments = append(arguments, "-v", input.Voice)
			}
		} else {
			if strings.ToLower(filepath.Ext(input.Output)) != ".wav" {
				return toolError(call, "speech output_path must end in .wav"), nil
			}
			program, err = nativeExecutable("espeak-ng")
			if err != nil {
				program, err = nativeExecutable("espeak")
			}
			if err != nil {
				return toolError(call, "install a native speech synthesizer (espeak-ng)"), nil
			}
			arguments = []string{"-w", filepath.Join(directory, "speech.aiff"), "--stdin"}
			if input.Voice != "" {
				arguments = append(arguments, "-v", input.Voice)
			}
		}
		if _, err := nativeRun(ctx, d.root, input.Text, program, arguments...); err != nil {
			return toolError(call, err.Error()), nil
		}
		var truncated bool
		data, truncated, err = readFileLimited(filepath.Join(directory, "speech.aiff"), 32<<20)
		if truncated {
			err = errors.New("speech exceeds 32 MiB")
		}
	} else {
		if d.networkPolicy != "allow" {
			return toolError(call, "image generation requires workspace network policy allow"), nil
		}
		if strings.TrimSpace(input.Prompt) == "" || len(input.Prompt) > 32000 || len(input.Input) > 4 || len(input.Model) > 128 {
			return toolError(call, "prompt is required (max 32000 bytes); at most 4 input images"), nil
		}
		if strings.ToLower(filepath.Ext(input.Output)) != ".png" {
			return toolError(call, "image output_path must end in .png"), nil
		}
		if input.Model == "" {
			input.Model = "gpt-image-1"
		}
		if input.Size == "" {
			input.Size = "1024x1024"
		}
		if input.Size != "auto" && input.Size != "1024x1024" && input.Size != "1536x1024" && input.Size != "1024x1536" {
			return toolError(call, "unsupported image size"), nil
		}
		key := d.apiKey
		if key == "" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		if key == "" {
			return toolError(call, "OPENAI_API_KEY is not configured"), nil
		}
		endpoint := d.endpoint
		if endpoint == "" {
			endpoint = firstString(os.Getenv("AZEM_IMAGE_BASE_URL"), "https://api.openai.com/v1")
		}
		parsed, parseErr := url.Parse(endpoint)
		if parseErr != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost")) {
			return toolError(call, "image endpoint must be a trusted HTTPS base URL"), nil
		}
		var body bytes.Buffer
		contentType, operation := "application/json", "generations"
		if len(input.Input) == 0 {
			err = json.NewEncoder(&body).Encode(map[string]any{"model": input.Model, "prompt": input.Prompt, "size": input.Size, "n": 1})
		} else {
			operation = "edits"
			writer := multipart.NewWriter(&body)
			for _, field := range [][2]string{{"model", input.Model}, {"prompt", input.Prompt}, {"size", input.Size}, {"n", "1"}} {
				if err = writer.WriteField(field[0], field[1]); err != nil {
					break
				}
			}
			if err == nil {
				for _, path := range input.Input {
					imageData, readErr := nativeImageFile(d.root, path)
					if readErr != nil {
						err = readErr
						break
					}
					var part io.Writer
					header := make(textproto.MIMEHeader)
					header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "image[]", "filename": filepath.Base(path)}))
					header.Set("Content-Type", http.DetectContentType(imageData))
					part, err = writer.CreatePart(header)
					if err != nil {
						break
					}
					if _, err = part.Write(imageData); err != nil {
						break
					}
				}
			}
			if closeErr := writer.Close(); err == nil {
				err = closeErr
			}
			contentType = writer.FormDataContentType()
		}
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/images/"+operation, &body)
		if err != nil {
			return toolError(call, "invalid image request"), nil
		}
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("Content-Type", contentType)
		client := d.client
		if client == nil {
			client = netproxy.NewHTTPClient(3 * time.Minute)
		}
		copyClient := *client
		copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		response, err := copyClient.Do(request)
		if err != nil {
			return toolError(call, "image provider request failed"), nil
		}
		defer response.Body.Close()
		if response.StatusCode/100 != 2 {
			return toolError(call, fmt.Sprintf("image provider returned HTTP %d", response.StatusCode)), nil
		}
		payload, truncated, err := readLimited(response.Body, 16<<20)
		if err != nil || truncated {
			return toolError(call, "image response unreadable or larger than 16 MiB"), nil
		}
		var result struct {
			Data []struct {
				Image string `json:"b64_json"`
			} `json:"data"`
		}
		if json.Unmarshal(payload, &result) != nil || len(result.Data) != 1 {
			return toolError(call, "image provider returned no unique base64 image"), nil
		}
		data, err = base64.StdEncoding.DecodeString(result.Data[0].Image)
		if err == nil {
			_, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
			if decodeErr != nil || format != "png" {
				err = errors.New("provider did not return PNG image data")
			}
		}
	}
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	if len(data) == 0 || len(data) > 32<<20 {
		return toolError(call, "media output must contain 1 byte to 32 MiB"), nil
	}
	var imageResult tool.Result
	if d.operation == ToolGenerateImage {
		imageResult, err = nativeImageResult(call, data, input.Output)
		if err != nil || imageResult.IsError {
			return imageResult, err
		}
	}
	if err := writeNativeArtifact(d.root, input.Output, data); err != nil {
		return toolError(call, err.Error()), nil
	}
	if d.operation == ToolGenerateImage {
		return imageResult, nil
	}
	return nativeJSONResult(call, map[string]any{"path": input.Output, "bytes": len(data)})
}

func nativeImageFile(root, path string) ([]byte, error) {
	_, relative, info, err := secureReadPath(root, path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil, errors.New("image must be a regular workspace file at most 4 MiB")
	}
	data, err := nativeWorkspaceFile(root, relative, 4<<20)
	if err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return nil, errors.New("image dimensions exceed 32 million pixels")
	}
	return data, nil
}

func nativeImageResult(call tool.Call, data []byte, path string) (tool.Result, error) {
	if len(data) > 4<<20 {
		return toolError(call, "image exceeds 4 MiB model output limit"), nil
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	if int64(config.Width)*int64(config.Height) > 32_000_000 {
		return toolError(call, "image exceeds pixel limit"), nil
	}
	mediaType := "image/" + format
	structured, _ := json.Marshal(map[string]any{"path": path, "bytes": len(data), "width": config.Width, "height": config.Height})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: fmt.Sprintf("%s (%dx%d)", path, config.Width, config.Height), Structured: structured, Parts: []message.ContentPart{{Kind: message.ContentImage, Data: data, MediaType: mediaType, Filename: filepath.Base(path)}}}, nil
}

func nativeArtifactAvailable(workspace, path string) error {
	relative, err := workspaceRelativePath(workspace, path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	_, err = root.Stat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("output already exists: %s", path)
	}
	return err
}

func writeNativeArtifact(workspace, path string, data []byte) error {
	relative, err := workspaceRelativePath(workspace, path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(relative), 0700); err != nil {
		return err
	}
	file, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	err = errors.Join(writeErr, syncErr, closeErr)
	if err != nil {
		return errors.Join(err, root.Remove(relative))
	}
	return nil
}
