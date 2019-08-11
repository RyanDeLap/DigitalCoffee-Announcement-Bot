package main

import (
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	imap "github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
	"github.com/grokify/html-strip-tags-go"
)

var fromEmails []string = []string{
	"robinpowell@missouristate.edu",
	"ajaykatangur@missouristate.edu",
	"ryandelap98@gmail.com",
}

var blackListWords []string = []string{
	"Ryan",
	"De Lap",
	"Delap",
}

var messageTemplate string = "@everyone\r\n**{{.Subject}}**\r\nFrom: {{.SenderName}}\r\n\r\n```{{.MessageText}}```"

var imapClient *client.Client
var discordClient *discordgo.Session
var channelId string

type MailMessage struct {
	Subject     string
	From        string
	MessageText string
}

func main() {
	if len(os.Args) < 6 {
		fmt.Println("Usage: discord-bot <token> <channel id> <imap tls server> <imap username> <imap password>\r\n")
		return
	}

	var err error

	if _, err = strconv.Atoi(os.Args[2]); err != nil {
		log.Fatal("The channel ID provided is invalid")
	}

	channelId = os.Args[2]

	discordClient, err = discordgo.New("Bot " + os.Args[1])
	if err != nil {
		log.Fatal("Error creating discord session:", err)
	}

	err = discordClient.Open()
	if err != nil {
		log.Fatal("Error connecting to discord:", err)
	}

	discordClient.AddHandler(messageCreate)

	setupImap()

	go mailboxWatcher()

	// Wait here until CTRL-C or other term signal is received.
	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	fmt.Printf("\n")

	discordClient.Close()
}

func setupImap() {
	var err error
	// Connect to server
	imapClient, err = client.DialTLS(os.Args[3], nil)
	if err != nil {
		log.Fatal("Error connecting to mail server:", err)
	}

	if err := imapClient.Login(os.Args[4], os.Args[5]); err != nil {
		log.Fatal("Error logging into imap server:", err)
	}
}

func mailboxWatcher() {

	lastMessages := make([]string, 0)

	defer func() {
		fmt.Println("left function")
	}()
	for {
		fmt.Println("loop start")
		mbox, err := imapClient.Select("INBOX", false)
		if err != nil {
			log.Fatal(err)
			setupImap()
			continue
		}

		// Get the last 5 messages
		from := uint32(1)
		to := mbox.Messages
		if mbox.Messages > 5 {
			from = mbox.Messages - 4
		}

		seqset := new(imap.SeqSet)
		seqset.AddRange(from, to)

		section := &imap.BodySectionName{}
		messages := make(chan *imap.Message, 5)
		done := make(chan error, 1)
		go func() {
			done <- imapClient.Fetch(seqset, []imap.FetchItem{imap.FetchEnvelope, section.FetchItem()}, messages)
		}()

		for msg := range messages {
			r := msg.GetBody(section)
			if r == nil {
				log.Println("Server didn't return message body")
				break
			}

			// Create a new mail reader
			mr, err := mail.CreateReader(r)
			if err != nil {
				log.Println("Failed to create message reader:", err)
				break
			}

			if len(lastMessages) < 5 {
				fmt.Println("Adding subject to queue: " + msg.Envelope.Subject)
				lastMessages = append(lastMessages, msg.Envelope.Subject)
				continue //Just ignore all previous emails incase of duplicate.
			} else {
				//If our queue doesn't contain one of the messages, we know its a new message. Pop the old subject, add the new one.
				if contains(lastMessages, msg.Envelope.Subject) == false {
					fmt.Println("Popping oldest, adding newest. ")
					lastMessages = lastMessages[1:]
					lastMessages = append(lastMessages, msg.Envelope.Subject)
					fmt.Println(strings.Join(lastMessages, ", "))

				} else {
					fmt.Println("No changes detected...")
					continue //If we already have scanned our message before, don't bother doing it again.
				}

			}

			senderName := ""
			announceThisEmail := false
			for _, senderAddr := range msg.Envelope.From {
				address := strings.ToLower(senderAddr.MailboxName + "@" + senderAddr.HostName)
				senderName = senderAddr.PersonalName

				// check our list of valid emails
				for _, email := range fromEmails {
					fmt.Println(email)
					if strings.ToLower(email) == address {
						announceThisEmail = true
						fmt.Println("Announce should be true.")
						break
					}
				}

				if announceThisEmail {
					break
				}
			}

			if !announceThisEmail {
				continue
			}

			messageText := ""
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				} else if err != nil {
					log.Fatal(err)
				}

				switch h := p.Header.(type) {
				case *mail.InlineHeader:
					contentType, _, _ := h.ContentType()
					b, _ := ioutil.ReadAll(p.Body)
					if contentType != "text/html" {
						break
					}

					if messageText != "" {
						break
					}

					messageText = strip.StripTags(string(b))
					messageText = strings.Replace(messageText, "&nbsp;", " ", -1)
					messageText = strings.Replace(messageText, "\r", "", -1)
					messageText = strings.Replace(messageText, "\n ", " ", -1)
					messageText = html.UnescapeString(messageText)
					newLineRegex, _ := regexp.Compile("\n\n(\n*)")
					messageText = newLineRegex.ReplaceAllString(messageText, "\n\n")
					messageStartNewLine, _ := regexp.Compile("^(\n*)")
					messageText = messageStartNewLine.ReplaceAllString(messageText, "")
					messageEndNewLine, _ := regexp.Compile("(\n*)$")
					messageText = messageEndNewLine.ReplaceAllString(messageText, "")
				}
			}

			// subject = msg.Envelope.Subject
			// message = messageText

			send := true

			for _, word := range blackListWords {
				if strings.Contains(messageText, word) {
					send = false
				}
			}

			if send {
				sendMessageWithTemplate(MailMessage{
					From:        senderName,
					Subject:     msg.Envelope.Subject,
					MessageText: messageText,
				})
			}
		}

		if err := <-done; err != nil {
			log.Fatal(err)
		}

		fmt.Println("sleeping")
		time.Sleep(60 * time.Second)
	}
}

func sendMessageWithTemplate(msg MailMessage) {
	outMsg := strings.Replace(messageTemplate, "{{.Subject}}", msg.Subject, -1)
	outMsg = strings.Replace(outMsg, "{{.SenderName}}", msg.From, -1)
	outMsg = strings.Replace(outMsg, "{{.MessageText}}", msg.MessageText, -1)
	fmt.Println("Sending message", msg)
	discordClient.ChannelMessageSend(channelId, outMsg)
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {

	if m.Author.ID == s.State.User.ID {
		return
	}

	if m.Content == "!botstatus" {
		s.ChannelMessageSend(m.ChannelID, "I'm alive!")
	}

	if m.Content == "!source" {
		s.ChannelMessageSend(m.ChannelID, "https://github.com/RyanDeLap/DigitalCoffee-Announcement-Bot")
	}
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
