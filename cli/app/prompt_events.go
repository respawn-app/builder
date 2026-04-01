package app

import (
	"context"
	"errors"
	"io"

	askquestion "builder/server/tools/askquestion"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
	"github.com/google/uuid"
)

func startPendingPromptEvents(ctx context.Context, sub serverapi.PromptActivitySubscription, control client.PromptControlClient) (<-chan askEvent, func()) {
	out := make(chan askEvent, 16)
	if sub == nil || control == nil {
		close(out)
		return out, func() {}
	}
	pollCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(out)
		defer func() { _ = sub.Close() }()
		for {
			evt, err := sub.Next(pollCtx)
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					return
				}
				return
			}
			if evt.Type != clientui.PendingPromptEventPending {
				continue
			}
			select {
			case <-pollCtx.Done():
				return
			case out <- pendingPromptEvent(evt, control):
			}
		}
	}()
	return out, cancel
}

func pendingPromptEvent(item clientui.PendingPromptEvent, control client.PromptControlClient) askEvent {
	req := askquestion.Request{
		ID:                     item.PromptID,
		Question:               item.Question,
		Suggestions:            append([]string(nil), item.Suggestions...),
		RecommendedOptionIndex: item.RecommendedOptionIndex,
		Approval:               item.Approval,
	}
	if len(item.ApprovalOptions) > 0 {
		req.ApprovalOptions = make([]askquestion.ApprovalOption, 0, len(item.ApprovalOptions))
		for _, option := range item.ApprovalOptions {
			req.ApprovalOptions = append(req.ApprovalOptions, askquestion.ApprovalOption{Decision: askquestion.ApprovalDecision(option.Decision), Label: option.Label})
		}
	}
	reply := make(chan askReply, 1)
	go func() {
		result, ok := <-reply
		if !ok {
			return
		}
		if item.Approval {
			answerReq := serverapi.ApprovalAnswerRequest{ClientRequestID: uuid.NewString(), SessionID: item.SessionID, ApprovalID: item.PromptID}
			if result.err != nil {
				answerReq.ErrorMessage = result.err.Error()
			} else if result.response.Approval != nil {
				answerReq.Decision = clientui.ApprovalDecision(result.response.Approval.Decision)
				answerReq.Commentary = result.response.Approval.Commentary
			} else {
				answerReq.ErrorMessage = errors.New("approval response is required").Error()
			}
			_ = control.AnswerApproval(context.Background(), answerReq)
			return
		}
		answerReq := serverapi.AskAnswerRequest{ClientRequestID: uuid.NewString(), SessionID: item.SessionID, AskID: item.PromptID}
		if result.err != nil {
			answerReq.ErrorMessage = result.err.Error()
		} else {
			answerReq.Answer = result.response.Answer
			answerReq.SelectedOptionNumber = result.response.SelectedOptionNumber
			answerReq.FreeformAnswer = result.response.FreeformAnswer
		}
		_ = control.AnswerAsk(context.Background(), answerReq)
	}()
	return askEvent{req: req, reply: reply}
}
