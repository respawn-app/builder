package llm

import (
	"encoding/json"
	"strings"

	"builder/internal/shared/textutil"
	"builder/internal/tools"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

func buildResponsesInput(canonical []ResponseItem) []responses.ResponseInputItemUnionParam {
	items := make([]responses.ResponseInputItemUnionParam, 0, len(canonical))
	for _, item := range canonical {
		switch item.Type {
		case ResponseItemTypeMessage:
			if strings.TrimSpace(item.Content) == "" {
				continue
			}
			items = append(items, messageInput(string(item.Role), item.Content, item.Phase))
		case ResponseItemTypeFunctionCall:
			callID := textutil.FirstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
			if callID == "" {
				continue
			}
			items = append(items, responses.ResponseInputItemParamOfFunctionCall(normalizeToolArguments(string(item.Arguments)), callID, strings.TrimSpace(item.Name)))
		case ResponseItemTypeFunctionCallOutput:
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				continue
			}
			items = append(items, functionCallOutputInputItems(callID, item.Name, item.Output)...)
		case ResponseItemTypeReasoning:
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			reasoningParam := responses.ResponseReasoningItemParam{
				ID:      id,
				Summary: []responses.ResponseReasoningItemSummaryParam{},
			}
			for _, summary := range item.ReasoningSummary {
				text := strings.TrimSpace(summary.Text)
				if text == "" {
					continue
				}
				reasoningParam.Summary = append(reasoningParam.Summary, responses.ResponseReasoningItemSummaryParam{
					Text: text,
					Type: "summary_text",
				})
			}
			if encrypted := strings.TrimSpace(item.EncryptedContent); encrypted != "" {
				reasoningParam.EncryptedContent = param.NewOpt(encrypted)
			}
			items = append(items, responses.ResponseInputItemUnionParam{OfReasoning: &reasoningParam})
		case ResponseItemTypeCompaction:
			encrypted := strings.TrimSpace(item.EncryptedContent)
			if encrypted == "" {
				continue
			}
			compactionParam := responses.ResponseCompactionItemParam{EncryptedContent: encrypted}
			if id := strings.TrimSpace(item.ID); id != "" {
				compactionParam.ID = param.NewOpt(id)
			}
			items = append(items, responses.ResponseInputItemUnionParam{OfCompaction: &compactionParam})
		default:
			if len(item.Raw) == 0 || !json.Valid(item.Raw) {
				continue
			}
			items = append(items, param.Override[responses.ResponseInputItemUnionParam](item.Raw))
		}
	}
	return items
}

func messageInput(role, text string, phase MessagePhase) responses.ResponseInputItemUnionParam {
	role = strings.TrimSpace(role)
	if role == string(RoleAssistant) {
		content := []responses.ResponseOutputMessageContentUnionParam{{
			OfOutputText: &responses.ResponseOutputTextParam{
				Annotations: []responses.ResponseOutputTextAnnotationUnionParam{},
				Text:        text,
			},
		}}
		item := responses.ResponseInputItemParamOfOutputMessage(content, "", responses.ResponseOutputMessageStatusCompleted)
		if item.OfOutputMessage != nil && phase != "" {
			item.OfOutputMessage.Phase = responses.ResponseOutputMessagePhase(phase)
		}
		return item
	}

	inputRole := string(RoleUser)
	switch role {
	case string(RoleSystem), string(RoleDeveloper), string(RoleUser):
		inputRole = role
	}
	content := responses.ResponseInputMessageContentListParam{responses.ResponseInputContentParamOfInputText(text)}
	return responses.ResponseInputItemParamOfInputMessage(content, inputRole)
}

func normalizeToolArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return "{}"
	}
	if json.Valid([]byte(arguments)) {
		return arguments
	}
	quoted, _ := json.Marshal(arguments)
	return string(quoted)
}

func normalizeToolInput(arguments string) json.RawMessage {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(arguments)) {
		return json.RawMessage(arguments)
	}
	quoted, _ := json.Marshal(arguments)
	return quoted
}

func outputStringFromRaw(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return trimmed
}

func functionCallOutputInputItems(callID string, toolName string, raw json.RawMessage) []responses.ResponseInputItemUnionParam {
	if contentItems, ok := functionCallOutputContentItemsFromRaw(raw); ok {
		if strings.TrimSpace(toolName) == string(tools.ToolViewImage) {
			if promotedInputMessage, promoted := promoteFunctionOutputFilesToInputMessage(contentItems); promoted {
				return []responses.ResponseInputItemUnionParam{
					responses.ResponseInputItemParamOfFunctionCallOutput(callID, "attached file content"),
					responses.ResponseInputItemParamOfInputMessage(promotedInputMessage, string(RoleUser)),
				}
			}
		}
		return []responses.ResponseInputItemUnionParam{responses.ResponseInputItemParamOfFunctionCallOutput(callID, contentItems)}
	}
	return []responses.ResponseInputItemUnionParam{responses.ResponseInputItemParamOfFunctionCallOutput(callID, outputStringFromRaw(raw))}
}

func promoteFunctionOutputFilesToInputMessage(contentItems responses.ResponseFunctionCallOutputItemListParam) (responses.ResponseInputMessageContentListParam, bool) {
	out := make(responses.ResponseInputMessageContentListParam, 0, len(contentItems))
	hasInputFile := false

	for _, item := range contentItems {
		switch {
		case item.OfInputText != nil:
			out = append(out, responses.ResponseInputContentParamOfInputText(item.OfInputText.Text))
		case item.OfInputImage != nil:
			image := responses.ResponseInputImageParam{}
			detail := responses.ResponseInputImageDetailAuto
			switch item.OfInputImage.Detail {
			case responses.ResponseInputImageContentDetailLow:
				detail = responses.ResponseInputImageDetailLow
			case responses.ResponseInputImageContentDetailHigh:
				detail = responses.ResponseInputImageDetailHigh
			case responses.ResponseInputImageContentDetailAuto:
				detail = responses.ResponseInputImageDetailAuto
			}
			image.Detail = detail
			if item.OfInputImage.ImageURL.Valid() {
				image.ImageURL = item.OfInputImage.ImageURL
			}
			if item.OfInputImage.FileID.Valid() {
				image.FileID = item.OfInputImage.FileID
			}
			out = append(out, responses.ResponseInputContentUnionParam{OfInputImage: &image})
		case item.OfInputFile != nil:
			hasInputFile = true
			file := responses.ResponseInputFileParam{}
			if item.OfInputFile.FileData.Valid() {
				file.FileData = item.OfInputFile.FileData
			}
			if item.OfInputFile.FileID.Valid() {
				file.FileID = item.OfInputFile.FileID
			}
			if item.OfInputFile.FileURL.Valid() {
				file.FileURL = item.OfInputFile.FileURL
			}
			if item.OfInputFile.Filename.Valid() {
				file.Filename = item.OfInputFile.Filename
			}
			out = append(out, responses.ResponseInputContentUnionParam{OfInputFile: &file})
		}
	}

	if !hasInputFile || len(out) == 0 {
		return nil, false
	}
	return out, true
}

func functionCallOutputContentItemsFromRaw(raw json.RawMessage) (responses.ResponseFunctionCallOutputItemListParam, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	if len(arr) == 0 {
		return nil, false
	}

	out := make(responses.ResponseFunctionCallOutputItemListParam, 0, len(arr))
	for _, rawItem := range arr {
		item, ok := functionCallOutputContentItemFromRaw(rawItem)
		if !ok {
			return nil, false
		}
		out = append(out, item)
	}
	return out, true
}

func functionCallOutputContentItemFromRaw(raw json.RawMessage) (responses.ResponseFunctionCallOutputItemUnionParam, bool) {
	var item struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
		Detail   string `json:"detail"`
		FileID   string `json:"file_id"`
		FileData string `json:"file_data"`
		FileURL  string `json:"file_url"`
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return responses.ResponseFunctionCallOutputItemUnionParam{}, false
	}

	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "input_text":
		return responses.ResponseFunctionCallOutputItemUnionParam{
			OfInputText: &responses.ResponseInputTextContentParam{Text: item.Text},
		}, true
	case "input_image":
		image := responses.ResponseInputImageContentParam{}
		if v := strings.TrimSpace(item.ImageURL); v != "" {
			image.ImageURL = param.NewOpt(v)
		}
		if v := strings.TrimSpace(item.FileID); v != "" {
			image.FileID = param.NewOpt(v)
		}
		switch strings.ToLower(strings.TrimSpace(item.Detail)) {
		case "low":
			image.Detail = responses.ResponseInputImageContentDetailLow
		case "high":
			image.Detail = responses.ResponseInputImageContentDetailHigh
		case "auto":
			image.Detail = responses.ResponseInputImageContentDetailAuto
		}
		if !image.ImageURL.Valid() && !image.FileID.Valid() {
			return responses.ResponseFunctionCallOutputItemUnionParam{}, false
		}
		return responses.ResponseFunctionCallOutputItemUnionParam{OfInputImage: &image}, true
	case "input_file":
		file := responses.ResponseInputFileContentParam{}
		if v := strings.TrimSpace(item.FileData); v != "" {
			file.FileData = param.NewOpt(v)
		}
		if v := strings.TrimSpace(item.FileURL); v != "" {
			file.FileURL = param.NewOpt(v)
		}
		if v := strings.TrimSpace(item.FileID); v != "" {
			file.FileID = param.NewOpt(v)
		}
		if v := strings.TrimSpace(item.Filename); v != "" {
			file.Filename = param.NewOpt(v)
		}
		if !file.FileData.Valid() && !file.FileURL.Valid() && !file.FileID.Valid() {
			return responses.ResponseFunctionCallOutputItemUnionParam{}, false
		}
		return responses.ResponseFunctionCallOutputItemUnionParam{OfInputFile: &file}, true
	default:
		return responses.ResponseFunctionCallOutputItemUnionParam{}, false
	}
}
