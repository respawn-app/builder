package app

import (
	"context"
	"errors"
	"strings"
	"time"

	askquestion "builder/server/tools/askquestion"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
	"github.com/google/uuid"
)

const promptActivityResubscribeDelay = 250 * time.Millisecond

type promptActivitySubscriber func(context.Context) (serverapi.PromptActivitySubscription, error)

func startPendingPromptEvents(ctx context.Context, sub serverapi.PromptActivitySubscription, subscribe promptActivitySubscriber, control client.PromptControlClient) (<-chan askEvent, func()) {
	out := make(chan askEvent, 16)
	if sub == nil || subscribe == nil || control == nil {
		close(out)
		return out, func() {}
	}
	pollCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(out)
		current := sub
		pendingPromptIDs := make(map[string]struct{})
		for {
			evt, err := current.Next(pollCtx)
			if err != nil {
				_ = current.Close()
				if errors.Is(err, context.Canceled) {
					return
				}
				current, err = resubscribePromptActivity(pollCtx, subscribe)
				if err != nil {
					return
				}
				continue
			}
			if strings.TrimSpace(evt.PromptID) == "" {
				continue
			}
			switch evt.Type {
			case clientui.PendingPromptEventResolved:
				delete(pendingPromptIDs, evt.PromptID)
				continue
			case clientui.PendingPromptEventPending:
				if _, exists := pendingPromptIDs[evt.PromptID]; exists {
					continue
				}
				pendingPromptIDs[evt.PromptID] = struct{}{}
			default:
				continue
			}
			select {
			case <-pollCtx.Done():
				_ = current.Close()
				return
			case out <- pendingPromptEvent(evt, control):
			}
		}
	}()
	return out, cancel
}

func resubscribePromptActivity(ctx context.Context, subscribe promptActivitySubscriber) (serverapi.PromptActivitySubscription, error) {
	for {
		if !waitPromptActivityRetry(ctx) {
			return nil, ctx.Err()
		}
		sub, err := subscribe(ctx)
		if err == nil {
			return sub, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
	}
}

func waitPromptActivityRetry(ctx context.Context) bool {
	timer := time.NewTimer(promptActivityResubscribeDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
