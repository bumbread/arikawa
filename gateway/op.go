package gateway

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/diamondburned/arikawa/internal/json"
	"github.com/diamondburned/arikawa/internal/wsutil"
	"github.com/pkg/errors"
)

type OPCode uint8

const (
	DispatchOP            OPCode = 0 // recv
	HeartbeatOP           OPCode = 1 // send/recv
	IdentifyOP            OPCode = 2 // send...
	StatusUpdateOP        OPCode = 3 //
	VoiceStateUpdateOP    OPCode = 4 //
	VoiceServerPingOP     OPCode = 5 //
	ResumeOP              OPCode = 6 //
	ReconnectOP           OPCode = 7 // recv
	RequestGuildMembersOP OPCode = 8 // send
	InvalidSessionOP      OPCode = 9 // recv...
	HelloOP               OPCode = 10
	HeartbeatAckOP        OPCode = 11
	CallConnectOP         OPCode = 13
	GuildSubscriptionsOP  OPCode = 14
)

type OP struct {
	Code OPCode   `json:"op"`
	Data json.Raw `json:"d,omitempty"`

	// Only for Dispatch (op 0)
	Sequence  int64  `json:"s,omitempty"`
	EventName string `json:"t,omitempty"`
}

var ErrInvalidSession = errors.New("Invalid session")

func DecodeOP(driver json.Driver, ev wsutil.Event) (*OP, error) {
	if ev.Error != nil {
		return nil, ev.Error
	}

	var op *OP
	if err := driver.Unmarshal(ev.Data, &op); err != nil {
		return nil, errors.Wrap(err, "Failed to decode payload")
	}

	if op.Code == InvalidSessionOP {
		return op, ErrInvalidSession
	}

	return op, nil
}

func DecodeEvent(driver json.Driver,
	ev wsutil.Event, v interface{}) (OPCode, error) {

	op, err := DecodeOP(driver, ev)
	if err != nil {
		return 0, err
	}

	if err := driver.Unmarshal(op.Data, v); err != nil {
		return 0, errors.Wrap(err, "Failed to decode data")
	}

	return op.Code, nil
}

func AssertEvent(driver json.Driver,
	ev wsutil.Event, code OPCode, v interface{}) (*OP, error) {

	op, err := DecodeOP(driver, ev)
	if err != nil {
		return nil, err
	}

	if op.Code != code {
		return op, fmt.Errorf(
			"Unexpected OP Code: %d, expected %d (%s)",
			op.Code, code, op.Data,
		)
	}

	if err := driver.Unmarshal(op.Data, v); err != nil {
		return op, errors.Wrap(err, "Failed to decode data")
	}

	return op, nil
}

func HandleEvent(g *Gateway, data []byte) error {
	if len(data) == 0 {
		return ErrInvalidSession
	}

	// Parse the raw data into an OP struct
	var op *OP
	if err := g.Driver.Unmarshal(data, &op); err != nil {
		return errors.Wrap(err, "OP error: "+string(data))
	}

	return HandleOP(g, op)
}

func HandleOP(g *Gateway, op *OP) error {
	if g.OP != nil {
		g.OP <- op
	}

	switch op.Code {
	case HeartbeatAckOP:
		// Heartbeat from the server?
		g.Pacemaker.Echo()

	case HeartbeatOP:
		// Server requesting a heartbeat.
		return g.Pacemaker.Pace()

	case ReconnectOP:
		// Server requests to reconnect, die and retry.
		g.Reconnect()
		return nil

	case InvalidSessionOP:
		// Discord expects us to sleep for no reason
		time.Sleep(time.Duration(rand.Intn(5)+1) * time.Second)

		// Invalid session, respond with Identify.
		return g.Identify()

	case HelloOP:
		// What is this OP doing here???
		return nil

	case DispatchOP:
		// Set the sequence
		if op.Sequence > 0 {
			g.Sequence.Set(op.Sequence)
		}

		// Check if we know the event
		fn, ok := EventCreator[op.EventName]
		if !ok {
			return fmt.Errorf(
				"Unknown event %s: %s",
				op.EventName, string(op.Data),
			)
		}

		// Make a new pointer to the event
		var ev = fn()

		// Try and parse the event
		if err := g.Driver.Unmarshal(op.Data, ev); err != nil {
			return errors.Wrap(err, "Failed to parse event "+op.EventName)
		}

		// If the event is a ready, we'll want its sessionID
		if ev, ok := ev.(*ReadyEvent); ok {
			g.SessionID = ev.SessionID
		}

		// Throw the event into a channel, it's valid now.
		g.Events <- ev
		return nil

	default:
		return fmt.Errorf(
			"Unknown OP code %d (event %s)", op.Code, op.EventName)
	}

	return nil
}
