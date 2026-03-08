package proto

const (
	TypeJoin              = "join"
	TypeMove              = "move"
	TypeChallenge         = "challenge"
	TypeChallengeResponse = "challenge_response"
	TypeHeartbeat         = "heartbeat"
	TypeWelcome           = "welcome"
	TypeState             = "state"
	TypeDeltaState        = "delta_state"
	TypeChallengeRequest  = "challenge_request"
	TypeChallengeResult   = "challenge_result"
	TypeInfo              = "info"
)

type Message struct {
	Type              string             `json:"type"`
	Join              *Join              `json:"join,omitempty"`
	Move              *Move              `json:"move,omitempty"`
	Challenge         *Challenge         `json:"challenge,omitempty"`
	ChallengeResponse *ChallengeResponse `json:"challenge_response,omitempty"`
	Heartbeat         *Heartbeat         `json:"heartbeat,omitempty"`
	Welcome           *Welcome           `json:"welcome,omitempty"`
	State             *State             `json:"state,omitempty"`
	DeltaState        *DeltaState        `json:"delta_state,omitempty"`
	ChallengeRequest  *ChallengeRequest  `json:"challenge_request,omitempty"`
	ChallengeResult   *ChallengeResult   `json:"challenge_result,omitempty"`
	Info              *Info              `json:"info,omitempty"`
}

type Join struct {
	Name string `json:"name"`
}

type Move struct {
	DX int `json:"dx"`
	DY int `json:"dy"`
}

type Challenge struct {
	TargetID int `json:"target_id"`
}

type ChallengeResponse struct {
	RequestID int  `json:"request_id"`
	Accept    bool `json:"accept"`
}

type Heartbeat struct {
	AtUnix int64 `json:"at_unix"`
}

type Welcome struct {
	PlayerID int           `json:"player_id"`
	MapW     int           `json:"map_w"`
	MapH     int           `json:"map_h"`
	Players  []PlayerState `json:"players"`
}

type State struct {
	Players []PlayerState `json:"players"`
}

type DeltaState struct {
	Players    []PlayerState `json:"players"`
	RemovedIDs []int         `json:"removed_ids"`
}

type PlayerState struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	HP   int    `json:"hp"`
}

type ChallengeRequest struct {
	RequestID int    `json:"request_id"`
	FromID    int    `json:"from_id"`
	FromName  string `json:"from_name"`
}

type ChallengeResult struct {
	RequestID int    `json:"request_id"`
	Accepted  bool   `json:"accepted"`
	Message   string `json:"message"`
}

type Info struct {
	Message string `json:"message"`
}
