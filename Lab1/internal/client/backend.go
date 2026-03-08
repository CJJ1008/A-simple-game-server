package client

import "cs/internal/proto"

type EventType string

const (
	EventWelcome          EventType = "welcome"
	EventState            EventType = "state"
	EventDeltaState       EventType = "delta_state"
	EventInfo             EventType = "info"
	EventChallengeRequest EventType = "challenge_request"
	EventChallengeResult  EventType = "challenge_result"
)

type Event struct {
	Type             EventType
	Welcome          *proto.Welcome
	State            *proto.State
	DeltaState       *proto.DeltaState
	Info             string
	ChallengeRequest *proto.ChallengeRequest
	ChallengeResult  *proto.ChallengeResult
}

type Backend interface {
	Connect(addr, name string) error
	Close() error
	Events() <-chan Event
	Move(dx, dy int) error
	Challenge(targetID int) error
	RespondChallenge(requestID int, accept bool) error
}
