package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	_ "net/http/pprof"
	"sync"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/pion/webrtc/v3"
	"github.com/sourcegraph/jsonrpc2"
)

type Candidate struct {
	Target    int                  `json:"target"`
	Candidate *webrtc.ICECandidate `json:"candidate"`
}

type Response struct {
	Params *webrtc.SessionDescription `json:"params"`
	Result *webrtc.SessionDescription `json:"result"`
	Method string                     `json:"method"`
	Id     uint64                     `json:"id"`
	Sid    string                     `json:"sid"`
}

type Request struct {
	Method string           `json:"method"`
	Params *json.RawMessage `json:"params,omitempty"`
	ID     jsonrpc2.ID      `json:"id"`
	Sid    string           `json:"sid"`
}

type TrickleResponse struct {
	Params Trickle `json:"params"`
	Method string  `json:"method"`
}

type Conn struct {
	mu   sync.Mutex
	Conn *websocket.Conn
}

func (c *Conn) sendMess(messType int, data interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	reqBodyBytes := new(bytes.Buffer)
	json.NewEncoder(reqBodyBytes).Encode(data)

	messageBytes := reqBodyBytes.Bytes()
	return c.Conn.WriteMessage(messType, messageBytes)
}

//var PullPeers map[string]*JSONSignal

func ReadMessage(c *Conn, peers map[string]*JSONSignal, logger logr.Logger, done chan struct{}) {
	//defer close(done)
	for {
		fmt.Println("List peers", peers)
		_, mess, errRead := c.Conn.ReadMessage()
		if errRead != nil {
			break
		}

		var response Request
		json.Unmarshal(mess, &response)

		if response.Method == "answer" {
			fmt.Println("got answer")
			if peers[response.Sid] != nil {
				var negotiation Negotiation
				err := json.Unmarshal(*response.Params, &negotiation)
				if err != nil {
					logger.Error(err, "Err unmarshal")
				}
				if err := peers[response.Sid].SetRemoteDescription(negotiation.Desc); err != nil {
					logger.Error(err, "Err set remote answer")
				}
			}

		} else if response.Method == "join" {
			if peers[response.Sid] != nil {
				var join Join
				err := json.Unmarshal(*response.Params, &join)
				if err != nil {
					peers[response.Sid].Logger.Error(err, "connect: error parsing offer")
					break
				}
				if join.SID != "" {
					peers[response.Sid].OnOffer = func(offer *webrtc.SessionDescription) {
						offerJSON, err := json.Marshal(&Negotiation{
							Desc: *offer,
						})
						if err != nil {
							logger.Error(err, "Err marshal")
						}

						params := (*json.RawMessage)(&offerJSON)

						connectionUUID := uuid.New()
						connectionID := uint64(connectionUUID.ID())

						offerMessage := &Request{
							Method: "offer",
							Params: params,
							Sid:    response.Sid,
							ID: jsonrpc2.ID{
								IsString: false,
								Str:      "",
								Num:      connectionID,
							},
						}

						errSend := c.sendMess(websocket.TextMessage, offerMessage)
						if errSend != nil {
							logger.Error(errSend, "Err send mess")
						}

						// reqBodyBytes := new(bytes.Buffer)
						// json.NewEncoder(reqBodyBytes).Encode(offerMessage)

						// messageBytes := reqBodyBytes.Bytes()
						// c.WriteMessage(websocket.TextMessage, messageBytes)
					}

					peers[response.Sid].OnIceCandidate = func(candidate *webrtc.ICECandidateInit, target int) {
						if target == 0 {
							target = 1
						} else {
							target = 0
						}
						if candidate != nil {
							candidateJSON, err := json.Marshal(&Trickle{
								Candidate: *candidate,
								Target:    target,
							})

							params := (*json.RawMessage)(&candidateJSON)

							if err != nil {
								logger.Error(err, "Err candidate")
							}

							message := &Request{
								Method: "trickle",
								Params: params,
								Sid:    response.Sid,
							}

							errSend := c.sendMess(websocket.TextMessage, message)
							if errSend != nil {
								logger.Error(errSend, "Err send mess")
							}

							// reqBodyBytes := new(bytes.Buffer)
							// json.NewEncoder(reqBodyBytes).Encode(message)
							// messageBytes := reqBodyBytes.Bytes()
							// c.WriteMessage(websocket.TextMessage, messageBytes)
						}
					}

					err = peers[response.Sid].Join(join.SID, join.UID, join.Config)
					if err != nil {
						logger.Error(err, "Err join peer")
						break
					}

					answer, err := peers[response.Sid].Answer(join.Offer)
					if err != nil {
						logger.Error(err, "Err create answer")
						break
					}

					connectionUUID := uuid.New()
					connectionID := uint64(connectionUUID.ID())

					offerJSON, _ := json.Marshal(&Negotiation{
						Desc: *answer,
					})

					params := (*json.RawMessage)(&offerJSON)

					answerMessage := &Request{
						Method: "answer",
						Params: params,
						Sid:    response.Sid,
						ID: jsonrpc2.ID{
							IsString: false,
							Str:      "",
							Num:      connectionID,
						},
					}

					errSend := c.sendMess(websocket.TextMessage, answerMessage)
					if errSend != nil {
						logger.Error(errSend, "Err send mess")
					}

					// reqBodyBytes := new(bytes.Buffer)
					// json.NewEncoder(reqBodyBytes).Encode(answerMessage)
					// messageBytes := reqBodyBytes.Bytes()
					// c.WriteMessage(websocket.TextMessage, messageBytes)
				}
			}

		} else if response.Method == "offer" {
			fmt.Println("got offer")
			if peers[response.Sid] != nil {
				var negotiation Negotiation
				err := json.Unmarshal(*response.Params, &negotiation)
				answer, err := peers[response.Sid].Answer(negotiation.Desc)
				if err != nil {
					logger.Error(err, "Err create ans")
				}

				connectionUUID := uuid.New()
				connectionID := uint64(connectionUUID.ID())

				offerJSON, _ := json.Marshal(&Negotiation{
					Desc: *answer,
				})

				params := (*json.RawMessage)(&offerJSON)

				answerMessage := &Request{
					Method: "answer",
					Params: params,
					Sid:    response.Sid,
					ID: jsonrpc2.ID{
						IsString: false,
						Str:      "",
						Num:      connectionID,
					},
				}

				errSend := c.sendMess(websocket.TextMessage, answerMessage)
				if errSend != nil {
					logger.Error(errSend, "Err send mess")
				}

				// reqBodyBytes := new(bytes.Buffer)
				// json.NewEncoder(reqBodyBytes).Encode(answerMessage)
				// messageBytes := reqBodyBytes.Bytes()
				// c.WriteMessage(websocket.TextMessage, messageBytes)
			}

		} else if response.Method == "trickle" {

			if peers[response.Sid] != nil {
				var trickle Trickle
				if err := json.Unmarshal(*response.Params, &trickle); err != nil {
					logger.Error(err, "Err read trickle")
				}

				err := peers[response.Sid].Trickle(trickle.Candidate, trickle.Target)
				if err != nil {
					logger.Error(err, "Err add candidate")
				}
			}
		}
	}
}
