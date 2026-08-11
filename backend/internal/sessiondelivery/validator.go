package sessiondelivery

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var forbiddenDeliveryRootKeys = map[string]struct{}{
	"account_id":          {},
	"auth_file":           {},
	"model_mapping_chain": {},
	"openai_ws_mode":      {},
	"upstream_model":      {},
}

func ValidateDelivery(record *DeliveryRecord, publicModel string) error {
	if record == nil {
		return errors.New("delivery record is nil")
	}
	if !strings.HasPrefix(record.SessionID, "session_") {
		return errors.New("session_id must use the session_ public ID prefix")
	}
	if record.RequestID == "" || !strings.HasPrefix(record.RequestID, "req_") {
		return errors.New("request_id must use the req_ public ID prefix")
	}
	if record.Timestamp.IsZero() {
		return errors.New("timestamp is required")
	}
	if record.Metadata.HTTPStatus < 200 || record.Metadata.HTTPStatus >= 300 {
		return errors.New("delivery metadata must describe a successful request")
	}
	if record.Response.StatusCode < 200 || record.Response.StatusCode >= 300 {
		return errors.New("delivery response must describe a successful request")
	}
	if len(record.Response.Error) > 0 && string(record.Response.Error) != "null" {
		return errors.New("successful delivery record must not contain response.error")
	}

	request, err := decodeJSONObject(record.Request, "request")
	if err != nil {
		return err
	}
	if _, wrapped := request["body"]; wrapped {
		return errors.New("request must not be wrapped in body")
	}
	if rawString(request["model"]) != publicModel {
		return fmt.Errorf("request.model must be %q", publicModel)
	}
	if len(request["messages"]) == 0 || bytes.Equal(bytes.TrimSpace(request["messages"]), []byte("null")) {
		return errors.New("request.messages is required")
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		return errors.New("request.messages must be an array")
	}
	if len(messages) == 0 {
		return errors.New("request.messages must not be empty")
	}
	if err := rejectForbiddenRootKeys(request, "request"); err != nil {
		return err
	}

	response, err := decodeJSONObject(record.Response.ResponseData, "response.response_data")
	if err != nil {
		return err
	}
	if rawString(response["model"]) != publicModel {
		return fmt.Errorf("response.response_data.model must be %q", publicModel)
	}
	if rawString(response["type"]) != "message" {
		return errors.New("response.response_data.type must be message")
	}
	if rawString(response["role"]) != "assistant" {
		return errors.New("response.response_data.role must be assistant")
	}
	if !strings.HasPrefix(rawString(response["id"]), "msg_") {
		return errors.New("response.response_data.id must use the msg_ public ID prefix")
	}
	var content []json.RawMessage
	if err := json.Unmarshal(response["content"], &content); err != nil {
		return errors.New("response.response_data.content must be an array")
	}
	if len(response["stop_reason"]) == 0 {
		return errors.New("response.response_data.stop_reason is required")
	}
	if err := rejectForbiddenRootKeys(response, "response.response_data"); err != nil {
		return err
	}
	return nil
}

func ValidateDeliveryJSON(line []byte, publicModel string) error {
	if bytes.Contains(line, []byte("\n")) {
		return errors.New("a JSONL record must not contain a literal newline")
	}
	root, err := decodeJSONObject(line, "delivery")
	if err != nil {
		return err
	}
	if err := rejectUnknownKeys(root, map[string]struct{}{
		"session_id": {}, "request_id": {}, "timestamp": {}, "metadata": {}, "request": {}, "response": {},
	}, "delivery"); err != nil {
		return err
	}
	metadata, err := decodeJSONObject(root["metadata"], "metadata")
	if err != nil {
		return err
	}
	if err := rejectUnknownKeys(metadata, map[string]struct{}{
		"http_status": {}, "latency_ms": {}, "user_agent": {},
	}, "metadata"); err != nil {
		return err
	}
	response, err := decodeJSONObject(root["response"], "response")
	if err != nil {
		return err
	}
	if err := rejectUnknownKeys(response, map[string]struct{}{
		"status_code": {}, "response_data": {}, "error": {},
	}, "response"); err != nil {
		return err
	}
	var record DeliveryRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return fmt.Errorf("decode delivery JSON: %w", err)
	}
	return ValidateDelivery(&record, publicModel)
}

func rejectUnknownKeys(object map[string]json.RawMessage, allowed map[string]struct{}, field string) error {
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s contains unsupported field %q", field, key)
		}
	}
	return nil
}

func decodeJSONObject(raw json.RawMessage, field string) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, fmt.Errorf("%s must be a complete JSON object", field)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return nil, fmt.Errorf("decode %s: %w", field, err)
	}
	return object, nil
}

func rejectForbiddenRootKeys(object map[string]json.RawMessage, field string) error {
	for key := range forbiddenDeliveryRootKeys {
		if _, exists := object[key]; exists {
			return fmt.Errorf("%s contains forbidden internal field %q", field, key)
		}
	}
	return nil
}
