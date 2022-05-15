package webex_utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"
)

// GetClient returns a Webex client
func GetClient(logger logr.Logger) *webexteams.Client {
	c := webexteams.NewClient()
	token := getToken(logger)
	c.SetAuthToken(token)

	me, _, err := c.People.GetMe()
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get information about who I am. Err: %v", err))
		panic(1)
	}

	logger.Info(fmt.Sprintf("I am %s -- %s.", me.DisplayName, me.ID))

	return c
}

// GetRoom returns the Webex room
func GetRoom(c *webexteams.Client, roomName string, logger logr.Logger) (*webexteams.Room, error) {
	roomQueryParams := &webexteams.ListRoomsQueryParams{
		Max: 200,
	}
	rooms, _, err := c.Rooms.ListRooms(roomQueryParams)
	if err != nil {
		logger.Info(fmt.Sprintf("%v", err))
		return nil, err
	}

	for i := range rooms.Items {
		if rooms.Items[i].Title == roomName {
			return &rooms.Items[i], nil
		}
	}

	logger.Info(fmt.Sprintf("Did not find room: %s", roomName))
	return nil, nil
}

// CreateWebhook creates a webhook to list to messages on roomID
func CreateWebhook(c *webexteams.Client, webHookURL, roomID string, logger logr.Logger) (*webexteams.Webhook, error) {
	webhookRequest := &webexteams.WebhookCreateRequest{
		Name:      "Webhook - Test",
		TargetURL: webHookURL,
		Resource:  "messages",
		Event:     "created",
		Filter:    "roomId=" + roomID,
	}

	testWebhook, _, err := c.Webhooks.CreateWebhook(webhookRequest)
	if err != nil {
		logger.Info(fmt.Sprintf("%v", err))
		return nil, err
	}

	return testWebhook, nil
}

// DeleteWebhook deletes the webhook
func DeleteWebhook(c *webexteams.Client, webhookID string, logger logr.Logger) error {
	_, err := c.Webhooks.DeleteWebhook(webhookID)
	if err != nil {
		logger.Info(fmt.Sprintf("%v", err))
		return err
	}

	return nil
}

func ListWebhook(c *webexteams.Client, logger logr.Logger) {
	queryParams := &webexteams.ListWebhooksQueryParams{}
	_, _, err := c.Webhooks.ListWebhooks(queryParams)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to list webhook %v", err))
		return
	}
}

func GetUser(c *webexteams.Client, username string, logger logr.Logger) (*webexteams.Person, error) {
	queryParams := &webexteams.ListPeopleQueryParams{
		Email: username,
		Max:   10,
	}

	people, _, err := c.People.ListPeople(queryParams)
	if err != nil {
		logger.Info(fmt.Sprintf("%v", err))
		return nil, err
	}

	if len(people.Items) != 1 {
		logger.Info(fmt.Sprintf("Found more than one person with username %s --- %v", username, people.Items))
		return nil, nil
	}

	return &people.Items[0], nil
}

// GetMessages returns the last N messages posted in the Webex room
func GetMessages(c *webexteams.Client, roomID string, logger logr.Logger) (*webexteams.Messages, error) {
	// GET messages
	messageQueryParams := &webexteams.ListMessagesQueryParams{
		RoomID:          roomID,
		MentionedPeople: "me", // bot needs this
		Max:             5,    // only at most last 5 messages sent to bot will be answered
	}

	messages, _, err := c.Messages.ListMessages(messageQueryParams)
	if err != nil {
		logger.Info(fmt.Sprintf("%v", err))
		return nil, err
	}

	return messages, nil
}

func sendMessage(c *webexteams.Client, message *webexteams.MessageCreateRequest,
	roomID string, logger logr.Logger) error {
	msg, resp, err := c.Messages.CreateMessage(message)
	if err != nil {
		logger.Info(fmt.Sprintf("%v", err))
		return err
	}

	logger.V(10).Info(fmt.Sprintf("response: %s", string(resp.Body())))

	logger.Info(fmt.Sprintf("Message ID %s", msg.ID))

	return nil
}

func SendMessageWithCard(c *webexteams.Client, roomID string, logger logr.Logger) error {
	jsonMap := make(map[string]interface{})
	err := json.Unmarshal([]byte(card), &jsonMap)
	if err != nil {
		panic(err)
	}

	message := &webexteams.MessageCreateRequest{
		RoomID:   roomID,
		Markdown: "did not understand the message",
		Attachments: []webexteams.Attachment{
			{
				Content:     jsonMap,
				ContentType: "application/vnd.microsoft.card.adaptive",
			},
		},
	}

	return sendMessage(c, message, roomID, logger)
}

// SendMessageWithGraph sends message to roomID with graph attached
func SendMessageWithGraphs(c *webexteams.Client, roomID, text string, paths []string,
	logger logr.Logger) error {

	message := &webexteams.MessageCreateRequest{
		Markdown: text,
		RoomID:   roomID,
	}

	for i := range paths {
		filename := filepath.Base(paths[i])
		file, err := os.Open(paths[i])
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to read file %s", paths[i]))
		} else {
			webexFile := webexteams.File{
				Name:   filename,
				Reader: file,
			}
			ext := filepath.Ext(filename)
			if ext == ".png" {
				webexFile.ContentType = "image/png"
			}
			message.Files = append(message.Files, webexFile)
		}
		defer file.Close()
	}

	return sendMessage(c, message, roomID, logger)
}

// SendMessage sends message to roomID
func SendMessage(c *webexteams.Client, roomID, text string,
	logger logr.Logger) error {
	message := &webexteams.MessageCreateRequest{
		Markdown: text,
		RoomID:   roomID,
	}
	return sendMessage(c, message, roomID, logger)
}

// getToken returns the Webex Auth Token
func getToken(logger logr.Logger) string {
	// Expect auth token to be stored in this env variable
	// To get auth token go to https://developer.webex.com/docs/getting-started
	envVar := "WEBEX_AUTH_TOKEN"
	token, ok := os.LookupEnv(envVar)
	if !ok {
		logger.Info(fmt.Sprintf("Env variable %s supposed to contain webex auth token not found", envVar))
		panic(1)
	}

	if token == "" {
		logger.Info(fmt.Sprintf("Env variable %s supposed to contain webex auth token is empty", envVar))
		panic(1)
	}

	return token
}
