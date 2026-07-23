package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yasyf/cc-review/internal/persistedjson"
)

const (
	unmatchedSchemaIdentity    = "dev.yasyf.cc-review.ai-request-unmatched"
	unmatchedSchemaDescriptor  = "payload:array<unmatched{pattern:string,why:string}>"
	unmatchedSchemaFingerprint = "dev.yasyf.cc-review.ai-request-unmatched.6b5c3807f383a671c52be520faa0176d1ee41286720c8f20fc10cf6da897f6d0"

	changesSchemaIdentity    = "dev.yasyf.cc-review.ai-request-changes"
	changesSchemaDescriptor  = "payload:array<change{path:string,reason:string,prior:{reviewed:bool,hidden:bool,fingerprint:string},applied:{reviewed:bool,hidden:bool}}>"
	changesSchemaFingerprint = "dev.yasyf.cc-review.ai-request-changes.654684ed37478d762cb6aa142c9e388ce6d9dd2134c7cf00ceb67d6189b9872d"

	questionSchemaIdentity    = "dev.yasyf.cc-review.ai-request-question"
	questionSchemaDescriptor  = "payload:{body:string,ask:null|ask{header:string,multiSelect:bool,options:array<option{label:string,description:string,preview:string}>}}"
	questionSchemaFingerprint = "dev.yasyf.cc-review.ai-request-question.da4d167c4597317bf142649389ee02f35ff9b46bf4b14b338ed237e79ee7dcc4"

	answerSchemaIdentity    = "dev.yasyf.cc-review.ai-request-answer"
	answerSchemaDescriptor  = "payload:{text:string,askAnswer:null|askAnswer{selected:array<string>,other:string,notes:string}}"
	answerSchemaFingerprint = "dev.yasyf.cc-review.ai-request-answer.cdbb1bce7a24ab6494614b160519f8c5875a06fbc628977bb42468fd18c031ba"

	replyAskSchemaIdentity    = "dev.yasyf.cc-review.reply-ask"
	replyAskSchemaDescriptor  = "payload:ask{header:string,multiSelect:bool,options:array<option{label:string,description:string,preview:string}>}"
	replyAskSchemaFingerprint = "dev.yasyf.cc-review.reply-ask.0cf745b0a1bf84cfef07448f62cb447f24c15467ca3509c488798e5eb291fdd8"

	replyAskAnswerSchemaIdentity    = "dev.yasyf.cc-review.reply-ask-answer"
	replyAskAnswerSchemaDescriptor  = "payload:askAnswer{selected:array<string>,other:string,notes:string}"
	replyAskAnswerSchemaFingerprint = "dev.yasyf.cc-review.reply-ask-answer.ba871e4d9c7f29219da9ebac796d75abd9ac1108c891c0874ae06e0f22aa1df4"

	attributionSchemaIdentity    = "dev.yasyf.cc-review.attribution-ranges"
	attributionSchemaDescriptor  = "payload:array<range{start:int,end:int,turnId:int64}>"
	attributionSchemaFingerprint = "dev.yasyf.cc-review.attribution-ranges.8a522d9816ef5b6890152871a74caa190fc51c0c24a1f87e991d93f74355fe7d"
)

type unmatchedV1 struct {
	Pattern *string `json:"pattern"`
	Why     *string `json:"why"`
}

type priorStateV1 struct {
	Reviewed    *bool   `json:"reviewed"`
	Hidden      *bool   `json:"hidden"`
	Fingerprint *string `json:"fingerprint"`
}

type appliedStateV1 struct {
	Reviewed *bool `json:"reviewed"`
	Hidden   *bool `json:"hidden"`
}

type changeV1 struct {
	Path    *string         `json:"path"`
	Reason  *string         `json:"reason"`
	Prior   *priorStateV1   `json:"prior"`
	Applied *appliedStateV1 `json:"applied"`
}

type askOptionV1 struct {
	Label       *string `json:"label"`
	Description *string `json:"description"`
	Preview     *string `json:"preview"`
}

type askV1 struct {
	Header      *string        `json:"header"`
	MultiSelect *bool          `json:"multiSelect"`
	Options     *[]askOptionV1 `json:"options"`
}

type askAnswerV1 struct {
	Selected *[]string `json:"selected"`
	Other    *string   `json:"other"`
	Notes    *string   `json:"notes"`
}

type questionV1 struct {
	Body *string         `json:"body"`
	Ask  json.RawMessage `json:"ask"`
}

type answerV1 struct {
	Text      *string         `json:"text"`
	AskAnswer json.RawMessage `json:"askAnswer"`
}

type attributionRangeV1 struct {
	Start  *int   `json:"start"`
	End    *int   `json:"end"`
	TurnID *int64 `json:"turnId"`
}

func encodeUnmatched(values []Unmatched) (string, error) {
	if values == nil {
		return "", errors.New("unmatched must be a non-nil array")
	}
	payload := make([]unmatchedV1, len(values))
	for i, value := range values {
		payload[i] = unmatchedV1{Pattern: ptr(value.Pattern), Why: ptr(value.Why)}
	}
	return encodeEnvelope(unmatchedSchemaIdentity, unmatchedSchemaFingerprint, payload)
}

func decodeUnmatched(data string) ([]Unmatched, error) {
	payload, err := persistedjson.Decode[[]unmatchedV1]([]byte(data), unmatchedSchemaIdentity, unmatchedSchemaFingerprint)
	if err != nil {
		return nil, err
	}
	values := make([]Unmatched, len(payload))
	for i, value := range payload {
		if value.Pattern == nil || value.Why == nil {
			return nil, fmt.Errorf("unmatched %d requires pattern and why", i)
		}
		values[i] = Unmatched{Pattern: *value.Pattern, Why: *value.Why}
	}
	return values, nil
}

func encodeChanges(values []AIChange) (string, error) {
	if values == nil {
		return "", errors.New("changes must be a non-nil array")
	}
	payload := make([]changeV1, len(values))
	for i, value := range values {
		payload[i] = changeV1{
			Path: ptr(value.Path), Reason: ptr(value.Reason),
			Prior: &priorStateV1{
				Reviewed: ptr(value.Prior.Reviewed), Hidden: ptr(value.Prior.Hidden), Fingerprint: ptr(value.Prior.Fingerprint),
			},
			Applied: &appliedStateV1{Reviewed: ptr(value.Applied.Reviewed), Hidden: ptr(value.Applied.Hidden)},
		}
	}
	return encodeEnvelope(changesSchemaIdentity, changesSchemaFingerprint, payload)
}

func decodeChanges(data string) ([]AIChange, error) {
	payload, err := persistedjson.Decode[[]changeV1]([]byte(data), changesSchemaIdentity, changesSchemaFingerprint)
	if err != nil {
		return nil, err
	}
	values := make([]AIChange, len(payload))
	for i, value := range payload {
		if value.Path == nil || value.Reason == nil || value.Prior == nil || value.Applied == nil ||
			value.Prior.Reviewed == nil || value.Prior.Hidden == nil || value.Prior.Fingerprint == nil ||
			value.Applied.Reviewed == nil || value.Applied.Hidden == nil {
			return nil, fmt.Errorf("change %d is incomplete", i)
		}
		values[i] = AIChange{
			Path: *value.Path, Reason: *value.Reason,
			Prior: PriorState{
				Reviewed: *value.Prior.Reviewed, Hidden: *value.Prior.Hidden, Fingerprint: *value.Prior.Fingerprint,
			},
			Applied: AppliedState{Reviewed: *value.Applied.Reviewed, Hidden: *value.Applied.Hidden},
		}
	}
	return values, nil
}

func encodeQuestion(value AIQuestion) (string, error) {
	ask, err := encodeOptionalAsk(value.Ask)
	if err != nil {
		return "", err
	}
	return encodeEnvelope(questionSchemaIdentity, questionSchemaFingerprint, questionV1{Body: ptr(value.Body), Ask: ask})
}

func decodeQuestion(data string) (AIQuestion, error) {
	payload, err := persistedjson.Decode[questionV1]([]byte(data), questionSchemaIdentity, questionSchemaFingerprint)
	if err != nil {
		return AIQuestion{}, err
	}
	if payload.Body == nil || payload.Ask == nil {
		return AIQuestion{}, errors.New("question requires body and ask")
	}
	ask, err := decodeOptionalAsk(payload.Ask)
	if err != nil {
		return AIQuestion{}, err
	}
	return AIQuestion{Body: *payload.Body, Ask: ask}, nil
}

func encodeAnswer(value AIAnswer) (string, error) {
	answer, err := encodeOptionalAskAnswer(value.AskAnswer)
	if err != nil {
		return "", err
	}
	return encodeEnvelope(answerSchemaIdentity, answerSchemaFingerprint, answerV1{Text: ptr(value.Text), AskAnswer: answer})
}

func decodeAnswer(data string) (AIAnswer, error) {
	payload, err := persistedjson.Decode[answerV1]([]byte(data), answerSchemaIdentity, answerSchemaFingerprint)
	if err != nil {
		return AIAnswer{}, err
	}
	if payload.Text == nil || payload.AskAnswer == nil {
		return AIAnswer{}, errors.New("answer requires text and askAnswer")
	}
	answer, err := decodeOptionalAskAnswer(payload.AskAnswer)
	if err != nil {
		return AIAnswer{}, err
	}
	return AIAnswer{Text: *payload.Text, AskAnswer: answer}, nil
}

func encodeReplyAsk(value Ask) (string, error) {
	payload, err := encodeAskV1(value)
	if err != nil {
		return "", err
	}
	return encodeEnvelope(replyAskSchemaIdentity, replyAskSchemaFingerprint, payload)
}

func decodeReplyAsk(data string) (Ask, error) {
	payload, err := persistedjson.Decode[askV1]([]byte(data), replyAskSchemaIdentity, replyAskSchemaFingerprint)
	if err != nil {
		return Ask{}, err
	}
	return decodeAskV1(payload)
}

func encodeReplyAskAnswer(value AskAnswer) (string, error) {
	payload, err := encodeAskAnswerV1(value)
	if err != nil {
		return "", err
	}
	return encodeEnvelope(replyAskAnswerSchemaIdentity, replyAskAnswerSchemaFingerprint, payload)
}

func decodeReplyAskAnswer(data string) (AskAnswer, error) {
	payload, err := persistedjson.Decode[askAnswerV1]([]byte(data), replyAskAnswerSchemaIdentity, replyAskAnswerSchemaFingerprint)
	if err != nil {
		return AskAnswer{}, err
	}
	return decodeAskAnswerV1(payload)
}

func encodeAttributionRanges(values []AttributionRange) (string, error) {
	if values == nil {
		return "", errors.New("attribution ranges must be a non-nil array")
	}
	payload := make([]attributionRangeV1, len(values))
	for i, value := range values {
		payload[i] = attributionRangeV1{Start: ptr(value.Start), End: ptr(value.End), TurnID: ptr(value.TurnID)}
	}
	return encodeEnvelope(attributionSchemaIdentity, attributionSchemaFingerprint, payload)
}

func decodeAttributionRanges(data string) ([]AttributionRange, error) {
	payload, err := persistedjson.Decode[[]attributionRangeV1]([]byte(data), attributionSchemaIdentity, attributionSchemaFingerprint)
	if err != nil {
		return nil, err
	}
	values := make([]AttributionRange, len(payload))
	for i, value := range payload {
		if value.Start == nil || value.End == nil || value.TurnID == nil {
			return nil, fmt.Errorf("attribution range %d requires start, end, and turnId", i)
		}
		values[i] = AttributionRange{Start: *value.Start, End: *value.End, TurnID: *value.TurnID}
	}
	return values, nil
}

func encodeAskV1(value Ask) (askV1, error) {
	if value.Options == nil {
		return askV1{}, errors.New("ask options must be a non-nil array")
	}
	if err := value.Validate(); err != nil {
		return askV1{}, err
	}
	options := make([]askOptionV1, len(value.Options))
	for i, option := range value.Options {
		options[i] = askOptionV1{
			Label: ptr(option.Label), Description: ptr(option.Description), Preview: ptr(option.Preview),
		}
	}
	return askV1{Header: ptr(value.Header), MultiSelect: ptr(value.MultiSelect), Options: &options}, nil
}

func decodeAskV1(value askV1) (Ask, error) {
	if value.Header == nil || value.MultiSelect == nil || value.Options == nil {
		return Ask{}, errors.New("ask requires header, multiSelect, and options")
	}
	options := make([]AskOption, len(*value.Options))
	for i, option := range *value.Options {
		if option.Label == nil || option.Description == nil || option.Preview == nil {
			return Ask{}, fmt.Errorf("ask option %d requires label, description, and preview", i)
		}
		options[i] = AskOption{Label: *option.Label, Description: *option.Description, Preview: *option.Preview}
	}
	ask := Ask{Header: *value.Header, MultiSelect: *value.MultiSelect, Options: options}
	if err := ask.Validate(); err != nil {
		return Ask{}, err
	}
	return ask, nil
}

func encodeAskAnswerV1(value AskAnswer) (askAnswerV1, error) {
	if value.Selected == nil {
		return askAnswerV1{}, errors.New("ask answer selected must be a non-nil array")
	}
	selected := make([]string, len(value.Selected))
	copy(selected, value.Selected)
	return askAnswerV1{Selected: &selected, Other: ptr(value.Other), Notes: ptr(value.Notes)}, nil
}

func decodeAskAnswerV1(value askAnswerV1) (AskAnswer, error) {
	if value.Selected == nil || value.Other == nil || value.Notes == nil {
		return AskAnswer{}, errors.New("ask answer requires selected, other, and notes")
	}
	return AskAnswer{Selected: *value.Selected, Other: *value.Other, Notes: *value.Notes}, nil
}

func encodeOptionalAsk(value *Ask) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("null"), nil
	}
	payload, err := encodeAskV1(*value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func decodeOptionalAsk(data json.RawMessage) (*Ask, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var value askV1
	if err := persistedjson.DecodeValue(data, &value); err != nil {
		return nil, err
	}
	ask, err := decodeAskV1(value)
	if err != nil {
		return nil, err
	}
	return &ask, nil
}

func encodeOptionalAskAnswer(value *AskAnswer) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("null"), nil
	}
	payload, err := encodeAskAnswerV1(*value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func decodeOptionalAskAnswer(data json.RawMessage) (*AskAnswer, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var value askAnswerV1
	if err := persistedjson.DecodeValue(data, &value); err != nil {
		return nil, err
	}
	answer, err := decodeAskAnswerV1(value)
	if err != nil {
		return nil, err
	}
	return &answer, nil
}

func encodeEnvelope[Payload any](identity, fingerprint string, payload Payload) (string, error) {
	encoded, err := persistedjson.Encode(identity, fingerprint, payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func ptr[Value any](value Value) *Value { return &value }
