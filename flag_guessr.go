package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"flag-guessr/data"
	"flag-guessr/util"
	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/log"
)

func main() {
	data.PopulateCountryMap()

	log.SetLevel(log.LevelInfo)
	log.Info("starting the bot...")
	log.Info("disgo version: ", disgo.Version)

	client, err := disgo.New(os.Getenv("FLAG_GUESSR_TOKEN"),
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentsNone),
			gateway.WithPresenceOpts(gateway.WithWatchingActivity("your guesses"))),
		bot.WithCacheConfigOpts(cache.WithCacheFlags(cache.FlagsNone)),
		bot.WithEventListeners(&events.ListenerAdapter{
			OnApplicationCommandInteraction: onCommand,
			OnComponentInteraction:          onButton,
			OnModalSubmit:                   onModal,
		}))
	if err != nil {
		log.Fatal("error while building disgo instance: ", err)
	}

	defer client.Close(context.TODO())

	if client.OpenGateway(context.TODO()) != nil {
		log.Fatalf("error while connecting to the gateway: %s", err)
	}

	log.Infof("flag guessr bot is now running.")
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-s
}

func onCommand(event *events.ApplicationCommandInteractionCreate) {
	interactionData := event.SlashCommandInteractionData()
	if interactionData.CommandName() == "flag" {
		difficulty := interactionData.Int("difficulty")
		_ = event.CreateMessage(util.GetCountryCreate(event.User(), util.GameDifficulty(difficulty), 0))
	}
}

func onButton(event *events.ComponentInteractionCreate) {
	var stateData util.ButtonStateData
	buttonID := event.Data.CustomID()
	_ = json.Unmarshal([]byte(buttonID), &stateData)

	actionType := stateData.ActionType
	cca := stateData.Cca
	country := data.CountryMap[cca]
	name := country.Name.Common
	messageBuilder := discord.NewMessageCreateBuilder()
	if actionType == util.ActionTypeDetails {
		err := event.CreateMessage(messageBuilder.
			SetContentf("Viewing details for **%s** %s %s", name, country.Flag, util.GetCountryInfo(country)).
			SetEphemeral(true).
			Build())
		if err != nil {
			log.Error("there was an error while creating details message: ", err)
		}
		return
	}
	user := event.User()
	if stateData.UserID != user.ID {
		err := event.CreateMessage(messageBuilder.
			SetContent("You can't interact with games of other users! Launch your own game by using </flag:1007718785345667284>.").
			SetEphemeral(true).
			Build())
		if err != nil {
			log.Error("there was an error while creating error message: ", err)
		}
		return
	}
	client := event.Client().Rest()
	switch actionType {
	case util.ActionTypeGuess:
		marshalledData, _ := json.Marshal(util.ModalStateData{
			Difficulty: stateData.Difficulty,
			Cca:        cca,
			Streak:     stateData.Streak,
		})
		err := event.CreateModal(discord.NewModalCreateBuilder().
			SetCustomID(string(marshalledData)).
			SetTitle("Guess the country!").
			AddActionRow(discord.NewShortTextInput("input", "Country name").
				WithPlaceholder("This field is case-insensitive.").
				WithRequired(true)).
			Build())
		if err != nil {
			log.Error("there was an error while creating modal: ", err)
		}
	case util.ActionTypeNewCountry:
		util.SendGameUpdates(util.NewCountryData{
			Interaction:     event,
			User:            user,
			FollowupContent: fmt.Sprintf("You skipped a country. It was **%s**. %s", name, country.Flag),
			Difficulty:      stateData.Difficulty,
			Cca:             cca,
			Client:          client,
		})
	case util.ActionTypeDelete:
		err := client.DeleteMessage(event.ChannelID(), event.Message.ID)
		if err != nil {
			log.Error("there was an error while deleting message: ", err)
		}
	case util.ActionTypeHint:
		hintType := stateData.HintType
		var hint string
		switch hintType {
		case util.HintTypePopulation:
			hint = fmt.Sprintf("The population of this country is %s.", util.FormatPopulation(country))
		case util.HintTypeTlds:
			tlds := country.Tlds
			if len(tlds) == 0 {
				hint = "This country has no Top Level Domains."
			} else {
				hint = fmt.Sprintf("The Top Level Domains of this country are **%s**.", strings.Join(tlds, ", "))
			}
		case util.HintTypeCapitals:
			capitals := country.Capitals
			if len(capitals) == 0 {
				hint = "This country has no capitals."
			} else {
				hint = fmt.Sprintf("The capitals of this country are **%s**.", strings.Join(capitals, ", "))
			}
		}
		stateData.HintType = hintType + 1
		err := event.UpdateMessage(discord.NewMessageUpdateBuilder().
			AddActionRow(util.GetGuessButtons(stateData)...).
			Build())
		if err != nil {
			log.Error("there was an error while updating message after hint usage: ", err)
		}
		_, err = client.CreateFollowupMessage(event.ApplicationID(), event.Token(),
			discord.NewMessageCreateBuilder().
				SetContent(hint).
				SetEphemeral(true).
				Build())
		if err != nil {
			log.Error("there was an error while creating hint message: ", err)
		}
	}
}

func onModal(event *events.ModalSubmitInteractionCreate) {
	eventData := event.Data

	var stateData util.ModalStateData
	modalID := eventData.CustomID
	_ = json.Unmarshal([]byte(modalID), &stateData)

	difficulty := stateData.Difficulty
	countryCca := stateData.Cca
	countryInput := eventData.Text("input")
	countryInputLow := strings.TrimSpace(strings.ToLower(countryInput))
	country := data.CountryMap[countryCca]
	countryName := country.Name
	countryCommonName := countryName.Common
	streak := stateData.Streak
	newCountryData := util.NewCountryData{
		Interaction: event,
		User:        event.User(),
		Difficulty:  difficulty,
		Cca:         countryCca,
		Client:      event.Client().Rest(),
	}
	if countryInputLow == strings.ToLower(countryCommonName) || countryInputLow == strings.ToLower(countryName.Official) {
		newCountryData.FollowupContent = fmt.Sprintf("Your guess was **correct**! It was **%s**. %s", countryCommonName, country.Flag)
		newCountryData.Streak = streak + 1
		util.SendGameUpdates(newCountryData)
	} else {
		if difficulty == util.GameDifficultyNormal {
			err := event.CreateMessage(discord.NewMessageCreateBuilder().
				SetContent("Your guess was **incorrect**. Please try again.").
				SetEphemeral(true).
				Build())
			if err != nil {
				log.Error("there was an error while creating a followup: ", err)
			}
		} else if difficulty == util.GameDifficultyHard {
			if streak == 0 {
				newCountryData.FollowupContent = fmt.Sprintf("Your guess was **incorrect**. It was %s. %s", countryCommonName, country.Flag)
			} else {
				newCountryData.FollowupContent = fmt.Sprintf("Your guess was **incorrect** and you've lost your streak of **%d**! It was **%s**. %s", streak, countryCommonName, country.Flag)
			}
			util.SendGameUpdates(newCountryData)
		}
	}
}
