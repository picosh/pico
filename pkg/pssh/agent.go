package pssh

import (
	"errors"

	"golang.org/x/crypto/ssh"
)

// AgentRequestType is the session request a client sends to forward its
// agent, as ssh -A does.
const AgentRequestType = "auth-agent-req@openssh.com"

const agentChannelType = "auth-agent@openssh.com"

type agentRequestedKey struct{}

// AgentRequestHandler records that the client forwarded its agent, and
// rejects a repeat like OpenSSH does. It isn't a default; a server that uses
// forwarded agents registers it under AgentRequestType.
func AgentRequestHandler(_ *ssh.Request, sesh *SSHServerConnSession) (bool, func()) {
	if sesh.AgentRequested() {
		return false, nil
	}
	sesh.SetValue(agentRequestedKey{}, true)
	return true, nil
}

// AgentRequested reports whether the client forwarded its agent on this
// session.
func (s *SSHServerConnSession) AgentRequested() bool {
	requested, _ := s.Value(agentRequestedKey{}).(bool)
	return requested
}

// OpenAgentChannel opens a channel to the client's forwarded agent. Each
// agent connection needs its own channel.
func (s *SSHServerConnSession) OpenAgentChannel() (ssh.Channel, error) {
	if !s.AgentRequested() {
		return nil, errors.New("client did not forward an agent")
	}

	ch, reqs, err := s.Conn.OpenChannel(agentChannelType, nil)
	if err != nil {
		return nil, err
	}
	go ssh.DiscardRequests(reqs)
	return ch, nil
}
