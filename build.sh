#!/bin/sh

go get github.com/bwmarrin/discordgo
go get "github.com/emersion/go-imap/..."
go get "github.com/emersion/go-message/mail"

go build -o discord-bot ./src/*.go