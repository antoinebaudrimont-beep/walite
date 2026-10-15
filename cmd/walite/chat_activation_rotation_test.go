package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func sendRotatingActivation(t *testing.T, receiver *chatActivationReceiver, ticket string) chatActivationResponse {
	t.Helper()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: receiver.socketPath, Net: "unix"})
	if err != nil {
		t.Fatal("could not connect to synthetic receiver")
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal("could not set fixture deadline")
	}
	if err := json.NewEncoder(connection).Encode(chatActivationCommand{Version: chatActivationRenewalVersion, ActivationID: ticket}); err != nil {
		t.Fatal("could not write fixture request")
	}
	if err := connection.CloseWrite(); err != nil {
		t.Fatal("could not frame fixture request")
	}
	data, err := io.ReadAll(io.LimitReader(connection, 1025))
	if err != nil || len(data) > 1024 {
		t.Fatal("missing or oversized fixture response")
	}
	var response chatActivationResponse
	if json.Unmarshal(data, &response) != nil || response.Version != chatActivationRenewalVersion {
		t.Fatal("invalid fixture response")
	}
	if strings.Contains(string(data), "@lid") || strings.Contains(string(data), "@g.us") || strings.Contains(string(data), receiver.token) {
		t.Fatal("response disclosed identity or reusable authentication")
	}
	return response
}

func TestRotatingTicketRepeatedDirectGroupNavigationAndReplayProtection(t *testing.T) {
	receiver := testActivationReceiver(t, context.Background(), activationTestDirectory(t))
	tickets := make([]string, 2)
	expiries := make([]int64, 2)
	ids := []string{"synthetic-direct@lid", "synthetic-group@g.us"}
	for index, id := range ids {
		var ok bool
		tickets[index], expiries[index], ok = receiver.issueRenewableTicket(id)
		if !ok || expiries[index] <= time.Now().Unix() || expiries[index] > time.Now().Add(chatActivationTicketLifetime).Unix() {
			t.Fatal("invalid renewable ticket lifetime")
		}
	}
	for iteration := 0; iteration < 4; iteration++ {
		for index, id := range ids {
			old := tickets[index]
			// V2 tickets cannot be consumed through the legacy, no-response path.
			sendActivationBytes(t, receiver, activationTicketJSON(t, old))
			if len(receiver.activations) != 0 {
				t.Fatal("v2 ticket accepted as v1")
			}
			response := sendRotatingActivation(t, receiver, old)
			if response.Status != "queued" || response.ActivationID == old || len(response.ActivationID) != 64 || response.ExpiresAt != expiries[index] {
				t.Fatal("ticket did not rotate without extending expiry")
			}
			if request := <-receiver.activations; request.ChatID != id {
				t.Fatal("repeated ticket selected a different conversation")
			}
			tickets[index] = response.ActivationID
			if sendRotatingActivation(t, receiver, old).Status != "unavailable" || len(receiver.activations) != 0 {
				t.Fatal("consumed ticket was replayed")
			}
		}
	}
	if len(receiver.tickets) != 2 || len(receiver.ticketOrder) != 2 {
		t.Fatal("rotation grew the bounded ticket store")
	}
}

func TestRotatingTicketExpiryEvictionInstanceAndRestartIsolation(t *testing.T) {
	base := activationTestDirectory(t)
	first := testActivationReceiver(t, context.Background(), base)
	second := testActivationReceiver(t, context.Background(), base)
	ticket, _, _ := first.issueRenewableTicket("synthetic@lid")
	if sendRotatingActivation(t, second, ticket).Status != "unavailable" {
		t.Fatal("ticket crossed instances")
	}
	response := sendRotatingActivation(t, first, ticket)
	<-first.activations
	ticket = response.ActivationID
	first.mu.Lock()
	entry := first.tickets[ticket]
	entry.expiresAt = time.Now().Add(-time.Second)
	first.tickets[ticket] = entry
	first.mu.Unlock()
	response = sendRotatingActivation(t, first, ticket)
	if response.Status != "unavailable" || response.ActivationID != "" || len(first.activations) != 0 {
		t.Fatal("expired ticket was renewed or admitted")
	}
	ticket, _, _ = first.issueRenewableTicket("synthetic@lid")
	rotated := sendRotatingActivation(t, first, ticket).ActivationID
	<-first.activations
	for index := 0; index < chatActivationTicketCapacity; index++ {
		first.issueRenewableTicket("newer@lid")
	}
	if sendRotatingActivation(t, first, rotated).Status != "unavailable" || len(first.tickets) != chatActivationTicketCapacity {
		t.Fatal("rotation refreshed FIFO priority or eviction failed")
	}
	ticket, _, _ = first.issueRenewableTicket("restart@lid")
	oldPath := first.socketPath
	first.stop()
	restarted := testActivationReceiver(t, context.Background(), base)
	if restarted.socketPath == oldPath || sendRotatingActivation(t, restarted, ticket).Status != "unavailable" {
		t.Fatal("restart preserved a stale capability")
	}
}

func TestRotatingTicketQueueSaturationReturnsFreshCapabilityWithoutAdmission(t *testing.T) {
	receiver := testActivationReceiver(t, context.Background(), activationTestDirectory(t))
	for index := 0; index < chatActivationQueueCapacity; index++ {
		sendActivationBytes(t, receiver, activationJSON(t, receiver, "queue@lid"))
	}
	ticket, expiry, _ := receiver.issueRenewableTicket("retry@g.us")
	response := sendRotatingActivation(t, receiver, ticket)
	if response.Status != "busy" || response.ActivationID == "" || response.ActivationID == ticket || response.ExpiresAt != expiry {
		t.Fatal("busy response did not safely rotate")
	}
	if sendRotatingActivation(t, receiver, ticket).Status != "unavailable" {
		t.Fatal("busy ticket could be replayed")
	}
	for len(receiver.activations) != 0 {
		<-receiver.activations
	}
	response = sendRotatingActivation(t, receiver, response.ActivationID)
	if response.Status != "queued" || (<-receiver.activations).ChatID != "retry@g.us" {
		t.Fatal("fresh busy successor could not be used later")
	}
}
