package main

import (
	"fmt"
	"io"
	"sync"

	"github.com/golang/protobuf/ptypes"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// chatServer implements the Support gRPC service, for providing
// a capability to connect customers and support agents in real-time
// chat.
type chatServer struct {
	chatsBySession     map[string]*session
	chatsAwaitingAgent []string
	lastSession        int32
	mu                 sync.RWMutex
}

type session struct {
	Session
	active bool
	cust   *listener
	agents map[string]*listener
	mu     sync.RWMutex
}

type listener struct {
	ch  chan<- *ChatEntry
	ctx context.Context
}

func (l *listener) send(e *ChatEntry) {
	select {
	case l.ch <- e:
	case <-l.ctx.Done():
	}
}

func (s *session) copySession() *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &Session{
		SessionId:    s.SessionId,
		CustomerName: s.Session.CustomerName,
		History:      s.Session.History,
	}
}

func (s *chatServer) ChatCustomer(stream Support_ChatCustomerServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	cust := getCustomer(ctx)
	if cust == "" {
		return status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	var sess *session
	var ch chan *ChatEntry
	var chCancel context.CancelFunc
	cleanup := func() {
		if sess != nil {
			sess.mu.Lock()
			sess.cust = nil
			sess.mu.Unlock()
			chCancel()
			close(ch)
			go func() {
				// drain channel to prevent deadlock
				for range ch {
				}
			}()
		}
	}
	defer cleanup()
	for {
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		switch req := req.Req.(type) {
		case *ChatCustomerRequest_Init:
			if sess != nil {
				return status.Errorf(codes.FailedPrecondition, "already called init, currently in chat session %q", sess.SessionId)
			}
			sessionID := req.Init.ResumeSessionId
			if sessionID == "" {
				sess, ch, chCancel = s.newSession(ctx, cust)
			} else if sess, ch, chCancel = s.resumeSession(ctx, cust, sessionID); sess == nil {
				return status.Errorf(codes.FailedPrecondition, "cannot resume session %q; it is not an open session", sessionID)
			}
			err := stream.Send(&ChatCustomerResponse{
				Resp: &ChatCustomerResponse_Session{
					Session: sess.copySession(),
				},
			})

			if err != nil {
				return err
			}
			// monitor the returned channel, sending incoming agent messages down the pipe
			go func() {
				for {
					select {
					case entry, ok := <-ch:
						if !ok {
							return
						}
						if e, ok := entry.Entry.(*ChatEntry_AgentMsg); ok {
							stream.Send(&ChatCustomerResponse{
								Resp: &ChatCustomerResponse_Msg{
									Msg: e.AgentMsg,
								},
							})
						}
					case <-ctx.Done():
						return
					}
				}
			}()

		case *ChatCustomerRequest_Msg:
			if sess == nil {
				return status.Errorf(codes.FailedPrecondition, "never called init, no chat session for message")
			}

			entry := &ChatEntry{
				Date: ptypes.TimestampNow(),
				Entry: &ChatEntry_CustomerMsg{
					CustomerMsg: req.Msg,
				},
			}
			func() {
				sess.mu.Lock()
				sess.Session.History = append(sess.Session.History, entry)
				sess.mu.Unlock()

				sess.mu.RLock()
				defer sess.mu.RUnlock()
				for _, l := range sess.agents {
					l.send(entry)
				}
			}()

		case *ChatCustomerRequest_HangUp:
			if sess == nil {
				return status.Errorf(codes.FailedPrecondition, "never called init, no chat session to hang up")
			}
			s.closeSession(sess)
			cleanup()
			sess = nil

		default:
			return status.Error(codes.InvalidArgument, "unknown request type")
		}
	}
}

func (s *chatServer) ChatAgent(stream Support_ChatAgentServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	agent := getAgent(ctx)
	if agent == "" {
		return status.Error(codes.Unauthenticated, codes.Unauthenticated.String())
	}

	var sess *session
	var ch chan *ChatEntry
	var chCancel context.CancelFunc
	cleanup := func() {
		if sess != nil {
			sess.mu.Lock()
			delete(sess.agents, agent)
			if len(sess.agents) == 0 {
				s.mu.Lock()
				s.chatsAwaitingAgent = append(s.chatsAwaitingAgent, sess.SessionId)
				s.mu.Unlock()
			}
			sess.mu.Unlock()
			chCancel()
			close(ch)
			go func() {
				// drain channel to prevent deadlock
				for range ch {
				}
			}()
		}
	}
	defer cleanup()

	checkSession := func() {
		// see if session was concurrently closed
		if sess != nil {
			sess.mu.RLock()
			active := sess.active
			sess.mu.RUnlock()
			if !active {
				cleanup()
				sess = nil
			}
		}
	}

	for {
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		checkSession()

		switch req := req.Req.(type) {
		case *ChatAgentRequest_Accept:
			if sess != nil {
				return status.Errorf(codes.FailedPrecondition, "already called accept, currently in chat session %q", sess.SessionId)
			}
			sess, ch, chCancel = s.acceptSession(ctx, agent, req.Accept.SessionId)
			if sess == nil {
				return status.Errorf(codes.FailedPrecondition, "no session to accept")
			}
			err := stream.Send(&ChatAgentResponse{
				Resp: &ChatAgentResponse_AcceptedSession{
					AcceptedSession: sess.copySession(),
				},
			})
			if err != nil {
				return err
			}
			// monitor the returned channel, sending incoming agent messages down the pipe
			go func() {
				for {
					select {
					case entry, ok := <-ch:
						if !ok {
							return
						}

						if entry == nil {
							stream.Send(&ChatAgentResponse{
								Resp: &ChatAgentResponse_SessionEnded{
									SessionEnded: Void_VOID,
								},
							})
							continue
						}

						if agentMsg, ok := entry.Entry.(*ChatEntry_AgentMsg); ok {
							if agentMsg.AgentMsg.AgentName == agent {
								continue
							}
						}
						stream.Send(&ChatAgentResponse{
							Resp: &ChatAgentResponse_Msg{
								Msg: entry,
							},
						})
					case <-ctx.Done():
						return
					}
				}
			}()

		case *ChatAgentRequest_Msg:
			if sess == nil {
				return status.Errorf(codes.FailedPrecondition, "never called accept, no chat session for message")
			}

			entry := &ChatEntry{
				Date: ptypes.TimestampNow(),
				Entry: &ChatEntry_AgentMsg{
					AgentMsg: &AgentMessage{
						AgentName: agent,
						Msg:       req.Msg,
					},
				},
			}
			active := true
			func() {
				sess.mu.Lock()
				active = sess.active
				if active {
					sess.Session.History = append(sess.Session.History, entry)
				}
				sess.mu.Unlock()

				if !active {
					return
				}

				sess.mu.RLock()
				defer sess.mu.RUnlock()
				if sess.cust != nil {
					sess.cust.send(entry)
				}
				for otherAgent, l := range sess.agents {
					if otherAgent == agent {
						continue
					}
					l.send(entry)
				}
			}()
			if !active {
				return status.Errorf(codes.FailedPrecondition, "customer hung up on chat session %s", sess.SessionId)
			}

		case *ChatAgentRequest_LeaveSession:
			if sess == nil {
				return status.Errorf(codes.FailedPrecondition, "never called init, no chat session to hang up")
			}
			s.closeSession(sess)
			cleanup()
			sess = nil

		default:
			return status.Error(codes.InvalidArgument, "unknown request type")
		}
	}
}

func (s *chatServer) newSession(ctx context.Context, cust string) (*session, chan *ChatEntry, context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSession++
	id := fmt.Sprintf("%06d", s.lastSession)
	s.chatsAwaitingAgent = append(s.chatsAwaitingAgent, id)

	ch := make(chan *ChatEntry, 1)
	ctx, cancel := context.WithCancel(ctx)
	l := &listener{
		ch:  ch,
		ctx: ctx,
	}
	sess := session{
		active: true,
		Session: Session{
			SessionId:    id,
			CustomerName: cust,
		},
		cust: l,
	}
	s.chatsBySession[id] = &sess

	return &sess, ch, cancel
}

func (s *chatServer) resumeSession(ctx context.Context, cust, sessionID string) (*session, chan *ChatEntry, context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.chatsBySession[sessionID]
	if sess.CustomerName != cust {
		// customer cannot join chat that they did not start
		return nil, nil, nil
	}
	if !sess.active {
		// chat has been closed
		return nil, nil, nil
	}
	if sess.cust != nil {
		// customer is active in the chat in another stream!
		return nil, nil, nil
	}

	ch := make(chan *ChatEntry, 1)
	ctx, cancel := context.WithCancel(ctx)
	l := &listener{
		ch:  ch,
		ctx: ctx,
	}
	sess.cust = l
	return sess, ch, cancel
}

func (s *chatServer) closeSession(sess *session) {
	active := true
	func() {
		sess.mu.Lock()
		active = sess.active
		sess.active = false
		sess.mu.Unlock()

		if !active {
			// already closed
			return
		}

		sess.mu.RLock()
		defer sess.mu.RUnlock()
		for _, l := range sess.agents {
			l.send(nil)
		}
	}()

	if !active {
		// already closed
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.chatsBySession, sess.SessionId)
	for i, id := range s.chatsAwaitingAgent {
		if id == sess.SessionId {
			s.chatsAwaitingAgent = append(s.chatsAwaitingAgent[:i], s.chatsAwaitingAgent[i+1:]...)
			break
		}
	}
}

func (s *chatServer) acceptSession(ctx context.Context, agent, sessionID string) (*session, chan *ChatEntry, context.CancelFunc) {
	var sess *session
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		if len(s.chatsAwaitingAgent) == 0 {
			return
		}
		if sessionID == "" {
			sessionID = s.chatsAwaitingAgent[0]
			s.chatsAwaitingAgent = s.chatsAwaitingAgent[1:]
		} else {
			found := false
			for i, id := range s.chatsAwaitingAgent {
				if id == sessionID {
					found = true
					s.chatsAwaitingAgent = append(s.chatsAwaitingAgent[:i], s.chatsAwaitingAgent[i+1:]...)
					break
				}
			}
			if !found {
				return
			}
		}
		sess = s.chatsBySession[sessionID]
	}()
	if sess == nil {
		return nil, nil, nil
	}
	ch := make(chan *ChatEntry, 1)
	ctx, cancel := context.WithCancel(ctx)
	l := &listener{
		ch:  ch,
		ctx: ctx,
	}
	sess.mu.Lock()
	if sess.agents == nil {
		sess.agents = map[string]*listener{}
	}
	sess.agents[agent] = l
	sess.mu.Unlock()
	return sess, ch, cancel
}
