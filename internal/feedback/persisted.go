package feedback

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yasyf/cc-review/internal/persistedjson"
	"github.com/yasyf/cc-review/internal/store"
)

const (
	feedbackSchemaIdentity    = "dev.yasyf.cc-review.feedback"
	feedbackSchemaDescriptor  = "payload:{review_id:string,version:int,session_id:string,frozen_at:int64,threads:array<thread{comment_id:int64,file_path:string,side:string,start_line:int,end_line:int,line_content:string,body:string,status:string,replies:array<reply{id:int64,origin:string,kind:string,body:string,ask:null|ask{header:string,multiSelect:bool,options:array<option{label:string,description:string,preview:string}>},answered:bool,answer:string,ask_answer:null|askAnswer{selected:array<string>,other:string,notes:string},answered_via:string}>}>,open_questions:array<question{reply_id:int64,comment_id:int64,file_path:string,start_line:int,comment_body:string,question:string,ask:null|ask{header:string,multiSelect:bool,options:array<option{label:string,description:string,preview:string}>}}>}"
	feedbackSchemaFingerprint = "dev.yasyf.cc-review.feedback.3c5a42b31804aa4f3d056eb0fd17a8d415f287e6c5ecac4cb016f86ba4a83197"
)

type feedbackV1 struct {
	ReviewID      *string           `json:"review_id"`
	Version       *int              `json:"version"`
	SessionID     *string           `json:"session_id"`
	FrozenAt      *int64            `json:"frozen_at"`
	Threads       *[]threadV1       `json:"threads"`
	OpenQuestions *[]openQuestionV1 `json:"open_questions"`
}

type threadV1 struct {
	CommentID   *int64     `json:"comment_id"`
	FilePath    *string    `json:"file_path"`
	Side        *string    `json:"side"`
	StartLine   *int       `json:"start_line"`
	EndLine     *int       `json:"end_line"`
	LineContent *string    `json:"line_content"`
	Body        *string    `json:"body"`
	Status      *string    `json:"status"`
	Replies     *[]replyV1 `json:"replies"`
}

type replyV1 struct {
	ID          *int64          `json:"id"`
	Origin      *string         `json:"origin"`
	Kind        *string         `json:"kind"`
	Body        *string         `json:"body"`
	Ask         json.RawMessage `json:"ask"`
	Answered    *bool           `json:"answered"`
	Answer      *string         `json:"answer"`
	AskAnswer   json.RawMessage `json:"ask_answer"`
	AnsweredVia *string         `json:"answered_via"`
}

type openQuestionV1 struct {
	ReplyID     *int64          `json:"reply_id"`
	CommentID   *int64          `json:"comment_id"`
	FilePath    *string         `json:"file_path"`
	StartLine   *int            `json:"start_line"`
	CommentBody *string         `json:"comment_body"`
	Question    *string         `json:"question"`
	Ask         json.RawMessage `json:"ask"`
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

func encodeFeedback(value Feedback) ([]byte, error) {
	if value.Threads == nil || value.OpenQuestions == nil {
		return nil, errors.New("feedback threads and open_questions must be non-nil arrays")
	}
	threads := make([]threadV1, len(value.Threads))
	for i, thread := range value.Threads {
		if thread.Replies == nil {
			return nil, fmt.Errorf("thread %d replies must be a non-nil array", i)
		}
		replies := make([]replyV1, len(thread.Replies))
		for j, reply := range thread.Replies {
			ask, err := encodeOptionalAsk(reply.Ask)
			if err != nil {
				return nil, fmt.Errorf("thread %d reply %d ask: %w", i, j, err)
			}
			answer, err := encodeOptionalAskAnswer(reply.AskAnswer)
			if err != nil {
				return nil, fmt.Errorf("thread %d reply %d ask answer: %w", i, j, err)
			}
			replies[j] = replyV1{
				ID: ptr(reply.ID), Origin: ptr(reply.Origin), Kind: ptr(reply.Kind), Body: ptr(reply.Body), Ask: ask,
				Answered: ptr(reply.Answered), Answer: ptr(reply.Answer), AskAnswer: answer, AnsweredVia: ptr(reply.AnsweredVia),
			}
		}
		threads[i] = threadV1{
			CommentID: ptr(thread.CommentID), FilePath: ptr(thread.FilePath), Side: ptr(thread.Side),
			StartLine: ptr(thread.StartLine), EndLine: ptr(thread.EndLine), LineContent: ptr(thread.LineContent),
			Body: ptr(thread.Body), Status: ptr(thread.Status), Replies: &replies,
		}
	}
	questions := make([]openQuestionV1, len(value.OpenQuestions))
	for i, question := range value.OpenQuestions {
		ask, err := encodeOptionalAsk(question.Ask)
		if err != nil {
			return nil, fmt.Errorf("open question %d ask: %w", i, err)
		}
		questions[i] = openQuestionV1{
			ReplyID: ptr(question.ReplyID), CommentID: ptr(question.CommentID), FilePath: ptr(question.FilePath),
			StartLine: ptr(question.StartLine), CommentBody: ptr(question.CommentBody), Question: ptr(question.Question), Ask: ask,
		}
	}
	payload := feedbackV1{
		ReviewID: ptr(value.ReviewID), Version: ptr(value.Version), SessionID: ptr(value.SessionID), FrozenAt: ptr(value.FrozenAt),
		Threads: &threads, OpenQuestions: &questions,
	}
	return persistedjson.Encode(feedbackSchemaIdentity, feedbackSchemaFingerprint, payload)
}

func decodeFeedback(data []byte) (Feedback, error) {
	payload, err := persistedjson.Decode[feedbackV1](data, feedbackSchemaIdentity, feedbackSchemaFingerprint)
	if err != nil {
		return Feedback{}, err
	}
	if payload.ReviewID == nil || payload.Version == nil || payload.SessionID == nil || payload.FrozenAt == nil ||
		payload.Threads == nil || payload.OpenQuestions == nil {
		return Feedback{}, errors.New("feedback payload is incomplete")
	}
	threads := make([]Thread, len(*payload.Threads))
	for i, thread := range *payload.Threads {
		if thread.CommentID == nil || thread.FilePath == nil || thread.Side == nil || thread.StartLine == nil ||
			thread.EndLine == nil || thread.LineContent == nil || thread.Body == nil || thread.Status == nil || thread.Replies == nil {
			return Feedback{}, fmt.Errorf("thread %d is incomplete", i)
		}
		replies := make([]Reply, len(*thread.Replies))
		for j, reply := range *thread.Replies {
			if reply.ID == nil || reply.Origin == nil || reply.Kind == nil || reply.Body == nil || reply.Ask == nil ||
				reply.Answered == nil || reply.Answer == nil || reply.AskAnswer == nil || reply.AnsweredVia == nil {
				return Feedback{}, fmt.Errorf("thread %d reply %d is incomplete", i, j)
			}
			ask, err := decodeOptionalAsk(reply.Ask)
			if err != nil {
				return Feedback{}, fmt.Errorf("thread %d reply %d ask: %w", i, j, err)
			}
			answer, err := decodeOptionalAskAnswer(reply.AskAnswer)
			if err != nil {
				return Feedback{}, fmt.Errorf("thread %d reply %d ask answer: %w", i, j, err)
			}
			replies[j] = Reply{
				ID: *reply.ID, Origin: *reply.Origin, Kind: *reply.Kind, Body: *reply.Body, Ask: ask,
				Answered: *reply.Answered, Answer: *reply.Answer, AskAnswer: answer, AnsweredVia: *reply.AnsweredVia,
			}
		}
		threads[i] = Thread{
			CommentID: *thread.CommentID, FilePath: *thread.FilePath, Side: *thread.Side,
			StartLine: *thread.StartLine, EndLine: *thread.EndLine, LineContent: *thread.LineContent,
			Body: *thread.Body, Status: *thread.Status, Replies: replies,
		}
	}
	questions := make([]OpenQuestion, len(*payload.OpenQuestions))
	for i, question := range *payload.OpenQuestions {
		if question.ReplyID == nil || question.CommentID == nil || question.FilePath == nil || question.StartLine == nil ||
			question.CommentBody == nil || question.Question == nil || question.Ask == nil {
			return Feedback{}, fmt.Errorf("open question %d is incomplete", i)
		}
		ask, err := decodeOptionalAsk(question.Ask)
		if err != nil {
			return Feedback{}, fmt.Errorf("open question %d ask: %w", i, err)
		}
		questions[i] = OpenQuestion{
			ReplyID: *question.ReplyID, CommentID: *question.CommentID, FilePath: *question.FilePath,
			StartLine: *question.StartLine, CommentBody: *question.CommentBody, Question: *question.Question, Ask: ask,
		}
	}
	return Feedback{
		ReviewID: *payload.ReviewID, Version: *payload.Version, SessionID: *payload.SessionID, FrozenAt: *payload.FrozenAt,
		Threads: threads, OpenQuestions: questions,
	}, nil
}

func encodeOptionalAsk(value *store.Ask) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("null"), nil
	}
	if value.Options == nil {
		return nil, errors.New("options must be a non-nil array")
	}
	if err := value.Validate(); err != nil {
		return nil, err
	}
	options := make([]askOptionV1, len(value.Options))
	for i, option := range value.Options {
		options[i] = askOptionV1{Label: ptr(option.Label), Description: ptr(option.Description), Preview: ptr(option.Preview)}
	}
	return json.Marshal(askV1{Header: ptr(value.Header), MultiSelect: ptr(value.MultiSelect), Options: &options})
}

func decodeOptionalAsk(data json.RawMessage) (*store.Ask, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var value askV1
	if err := persistedjson.DecodeValue(data, &value); err != nil {
		return nil, err
	}
	if value.Header == nil || value.MultiSelect == nil || value.Options == nil {
		return nil, errors.New("ask requires header, multiSelect, and options")
	}
	options := make([]store.AskOption, len(*value.Options))
	for i, option := range *value.Options {
		if option.Label == nil || option.Description == nil || option.Preview == nil {
			return nil, fmt.Errorf("ask option %d is incomplete", i)
		}
		options[i] = store.AskOption{Label: *option.Label, Description: *option.Description, Preview: *option.Preview}
	}
	ask := store.Ask{Header: *value.Header, MultiSelect: *value.MultiSelect, Options: options}
	if err := ask.Validate(); err != nil {
		return nil, err
	}
	return &ask, nil
}

func encodeOptionalAskAnswer(value *store.AskAnswer) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("null"), nil
	}
	if value.Selected == nil {
		return nil, errors.New("selected must be a non-nil array")
	}
	selected := make([]string, len(value.Selected))
	copy(selected, value.Selected)
	return json.Marshal(askAnswerV1{Selected: &selected, Other: ptr(value.Other), Notes: ptr(value.Notes)})
}

func decodeOptionalAskAnswer(data json.RawMessage) (*store.AskAnswer, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var value askAnswerV1
	if err := persistedjson.DecodeValue(data, &value); err != nil {
		return nil, err
	}
	if value.Selected == nil || value.Other == nil || value.Notes == nil {
		return nil, errors.New("ask answer requires selected, other, and notes")
	}
	answer := store.AskAnswer{Selected: *value.Selected, Other: *value.Other, Notes: *value.Notes}
	return &answer, nil
}

func ptr[Value any](value Value) *Value { return &value }
