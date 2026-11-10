package openai

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestConvertImageEditRequestMultipart verifies that ConvertImageRequest
// re-serializes multipart image edit requests with all fields (including
// stream) and the file intact, both when the form was already parsed and when
// it must be re-parsed from the reusable body.
func TestConvertImageEditRequestMultipart(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newMultipartContext := func(t *testing.T, prompt string) *gin.Context {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("model", "gpt-image-1"))
		require.NoError(t, writer.WriteField("prompt", prompt))
		require.NoError(t, writer.WriteField("stream", "true"))
		require.NoError(t, writer.WriteField("partial_images", "3"))
		part, err := writer.CreateFormFile("image", "input.png")
		require.NoError(t, err)
		_, err = part.Write([]byte("fake image"))
		require.NoError(t, err)
		require.NoError(t, writer.Close())

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		return c
	}

	convertAndReplay := func(t *testing.T, c *gin.Context, prompt string) {
		info := &relaycommon.RelayInfo{
			RelayMode: relayconstant.RelayModeImagesEdits,
		}
		request := dto.ImageRequest{
			Model:  "gpt-image-1",
			Prompt: prompt,
			Stream: common.GetPointer(true),
		}

		converted, err := (&Adaptor{}).ConvertImageRequest(c, info, request)
		require.NoError(t, err)
		convertedBody, ok := converted.(*bytes.Buffer)
		require.True(t, ok)

		replayedRequest := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(convertedBody.Bytes()))
		replayedRequest.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
		require.NoError(t, replayedRequest.ParseMultipartForm(32<<20))

		require.Equal(t, "gpt-image-1", replayedRequest.PostForm.Get("model"))
		require.Equal(t, prompt, replayedRequest.PostForm.Get("prompt"))
		require.Equal(t, "true", replayedRequest.PostForm.Get("stream"))
		require.Equal(t, "3", replayedRequest.PostForm.Get("partial_images"))
		require.Len(t, replayedRequest.MultipartForm.File["image"], 1)

		file, err := replayedRequest.MultipartForm.File["image"][0].Open()
		require.NoError(t, err)
		defer file.Close()
		fileBytes, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, []byte("fake image"), fileBytes)
	}

	t.Run("with pre-parsed form", func(t *testing.T) {
		prompt := "edit this image"
		c := newMultipartContext(t, prompt)
		require.NoError(t, c.Request.ParseMultipartForm(32<<20))

		convertAndReplay(t, c, prompt)
	})

	t.Run("re-parses reusable body when form is missing", func(t *testing.T) {
		prompt := "edit without pre-parsed form"
		c := newMultipartContext(t, prompt)

		storage, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		c.Request.Body = io.NopCloser(storage)
		c.Request.MultipartForm = nil
		c.Request.PostForm = nil

		convertAndReplay(t, c, prompt)
	})
}

// TestConvertImageEditRequestAppliesParamOverride verifies that a channel
// parameter override written for the JSON image API also rewrites the text
// fields of a multipart edit, reports the overridden n for billing, and leaves
// the client's parsed form untouched for the next channel attempt.
func TestConvertImageEditRequestAppliesParamOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newEditContext := func(t *testing.T, fields map[string]string) *gin.Context {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for key, value := range fields {
			require.NoError(t, writer.WriteField(key, value))
		}
		part, err := writer.CreateFormFile("image", "input.png")
		require.NoError(t, err)
		_, err = part.Write([]byte("fake image"))
		require.NoError(t, err)
		require.NoError(t, writer.Close())

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		require.NoError(t, c.Request.ParseMultipartForm(32<<20))
		return c
	}

	tests := []struct {
		name       string
		fields     map[string]string
		override   map[string]any
		wantFields map[string]string
		wantN      *uint
	}{
		{
			name:       "override replaces and adds fields",
			fields:     map[string]string{"model": "gpt-image-1", "prompt": "edit", "n": "4", "response_format": "b64_json"},
			override:   map[string]any{"response_format": "url", "n": 1},
			wantFields: map[string]string{"prompt": "edit", "n": "1", "response_format": "url"},
			wantN:      common.GetPointer(uint(1)),
		},
		{
			name:       "override adds a field the client omitted",
			fields:     map[string]string{"model": "gpt-image-1", "prompt": "edit"},
			override:   map[string]any{"response_format": "url"},
			wantFields: map[string]string{"prompt": "edit", "response_format": "url"},
		},
		{
			name:       "no override forwards the client fields",
			fields:     map[string]string{"model": "gpt-image-1", "prompt": "edit", "n": "2"},
			wantFields: map[string]string{"prompt": "edit", "n": "2"},
			wantN:      common.GetPointer(uint(2)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newEditContext(t, tt.fields)
			info := &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeImagesEdits,
				ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: tt.override},
			}

			converted, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{Model: "gpt-image-1"})
			require.NoError(t, err)
			convertedBody, ok := converted.(*bytes.Buffer)
			require.True(t, ok)

			replayed := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(convertedBody.Bytes()))
			replayed.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
			require.NoError(t, replayed.ParseMultipartForm(32<<20))

			sent := make(map[string]string, len(replayed.PostForm))
			for key := range replayed.PostForm {
				if key != "model" {
					sent[key] = replayed.PostForm.Get(key)
				}
			}
			require.Equal(t, tt.wantFields, sent)
			require.Equal(t, tt.wantN, info.OutboundImageFormN)
			require.Len(t, replayed.MultipartForm.File["image"], 1)

			for key, value := range tt.fields {
				require.Equal(t, []string{value}, c.Request.MultipartForm.Value[key], "client field %q must stay as sent", key)
			}
		})
	}

	t.Run("override producing a non-integer n is rejected", func(t *testing.T) {
		c := newEditContext(t, map[string]string{"model": "gpt-image-1", "prompt": "edit"})
		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeImagesEdits,
			ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: map[string]any{"n": "many"}},
		}

		_, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{Model: "gpt-image-1"})
		require.Error(t, err)
	})
}
