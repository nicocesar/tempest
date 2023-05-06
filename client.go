package tempest

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/sugawarayuuta/sonnet"
)

type ClientOptions struct {
	ApplicationID              Snowflake                                  // The app's user id. (default: <nil>)
	PublicKey                  string                                     // Hash like key used to verify incoming payloads from Discord. (default: <nil>)
	Token                      string                                     // The auth token to use. Bot tokens should be prefixed with Bot (e.g. "Bot MTExIHlvdSAgdHJpZWQgMTEx.O5rKAA.dQw4w9WgXcQ_wpV-gGA4PSk_bm8"). Prefix-less bot tokens are deprecated. (default: <nil>)
	GlobalRequestLimit         uint16                                     // The maximum number of requests app can make to Discord API before reaching global rate limit. Default limit is 50 but big bots (over 100,000 guilds) receives bigger limits. (default: 50)
	MaxRequestsBeforeSweep     uint16                                     // The maximum number of REST requests after which app start clearing memory. Majority of Discord applications can hold it on default 100 but if your app handles like hundreds of commands each second then it's recommend increasing that limit. Increasing it will result in higher memory usage but reduce CPU usage. (default: 100)
	Cooldowns                  *ClientCooldownOptions                     // The built-in cooldown mechanic for commands. Skip this field if you don't want to use automatic cooldown system (it won't allocate any extra memory if it's not used). (default: <nil>)
	PreCommandExecutionHandler func(itx CommandInteraction) *ResponseData // Function to call after doing initial processing but before executing slash command. Allows to attach own, global logic to all slash commands (similar to routing). Return pointer to ResponseData struct if you want to send messageand stop execution or <nil> to continue. (default: <nil>)
	InteractionHandler         func(itx Interaction)                      // Function to call on all unhandled interactions. (default: <nil>)
}

type ClientCooldownOptions struct {
	Duration                time.Duration
	Ephemeral               bool                                                 // Whether message about being on cooldown should be ephemeral.
	CooldownResponse        func(user User, timeLeft time.Duration) ResponseData // Response object to reply to member/user on cooldown.
	MaxCooldownsBeforeSweep uint16                                               // The maximum number of cooldown entries to keep after which app start clearing memory. Majority of Discord applications can hold it on default 100 but if your app handles like hundreds of commands each second then it's recommend increasing that limit. Increasing it will result in higher memory usage but reduce CPU usage. (default: 100)
}

// Please avoid creating raw Client struct unless you know what you're doing. Use CreateClient function instead.
type Client struct {
	Rest                    Rest
	User                    User
	ApplicationID           Snowflake
	PublicKey               ed25519.PublicKey
	MaxCooldownsBeforeSweep uint16

	commands                   map[string]map[string]Command              // Search by command name, then subcommand name (if it's main command then provide "-" as subcommand name)
	queuedComponents           map[string]*(chan *Interaction)            // Map with all currently running button queues.
	preCommandExecutionHandler func(itx CommandInteraction) *ResponseData // From options, called before each slash command.
	interactionHandler         func(itx Interaction)                      // From options, called on all unhandled interactions.
	running                    bool                                       // Whether client's web server is already launched.

	cdrs              bool
	cdrDuration       time.Duration
	cdrEphemeral      bool
	cdrResponse       func(user User, timeLeft time.Duration) ResponseData
	cdrCooldowns      map[Snowflake]time.Time
	cdrMaxBeforeSweep uint16
	cdrSinceSweep     uint16
}

// Pings Discord API and returns time it took to get response.
func (client Client) Ping() time.Duration {
	start := time.Now()
	client.Rest.Request(http.MethodGet, "/gateway", nil)
	return time.Since(start)
}

// Makes client "listen" incoming component type interactions.
// When component custom id matches - it'll send back interaction through channel.
// On timeout - client will send <nil> through channel and automatically call close function.
//
// Warning! Don't try to acknowledge any component passed to this method, it'll be handled automatically.
//
// Warning! Listener will continue to work unless it timeouts or when calling close function that is returned to you with channel.
//
// Set timeout equal to 0 to make it last infinitely.
func (client Client) AwaitComponent(componentCustomIDs []string, timeout time.Duration) (chan *Interaction, func()) {
	signalChannel := make(chan *Interaction)
	closeFunction := func() {
		if signalChannel != nil {
			for _, key := range componentCustomIDs {
				delete(client.queuedComponents, key)
			}

			close(signalChannel)
			signalChannel = nil
		}
	}

	for _, key := range componentCustomIDs {
		client.queuedComponents[key] = &signalChannel
	}

	if timeout != 0 {
		time.AfterFunc(timeout, closeFunction)
	}

	return signalChannel, closeFunction
}

func (client Client) SendMessage(channelID Snowflake, content Message) (Message, error) {
	raw, err := client.Rest.Request(http.MethodPost, "/channels/"+channelID.String()+"/messages", content)
	if err != nil {
		return Message{}, err
	}

	res := Message{}
	err = sonnet.Unmarshal(raw, &res)
	if err != nil {
		return Message{}, errors.New("failed to parse received data from discord")
	}

	return res, nil
}

// Use that for simple text messages that won't be modified.
func (client Client) SendLinearMessage(channelID Snowflake, content string) (Message, error) {
	raw, err := client.Rest.Request(http.MethodPost, "/channels/"+channelID.String()+"/messages", Message{
		Content:    content,
		Embeds:     make([]*Embed, 1),
		Components: make([]*Component, 1),
	})
	if err != nil {
		return Message{}, err
	}

	res := Message{}
	err = sonnet.Unmarshal(raw, &res)
	if err != nil {
		return Message{}, errors.New("failed to parse received data from discord")
	}

	return res, nil
}

// Creates (or fetches if already exists) user's private text channel (DM) and tries to send message into it.
// Warning! Discord's user channels endpoint has huge rate limits so please reuse Message#ChannelID whenever possible.
func (client Client) SendPrivateMessage(userID Snowflake, content Message) (Message, error) {
	res := make(map[string]interface{}, 0)
	res["recipient_id"] = userID

	raw, err := client.Rest.Request(http.MethodPost, "/users/@me/channels", res)
	if err != nil {
		return Message{}, err
	}

	err = sonnet.Unmarshal(raw, &res)
	if err != nil {
		return Message{}, errors.New("failed to parse received data from discord")
	}

	channelID := StringToSnowflake(res["id"].(string))
	msg, err := client.SendMessage(channelID, content)
	msg.ChannelID = channelID // Just in case.

	return msg, err
}

func (client Client) EditMessage(channelID Snowflake, messageID Snowflake, content Message) error {
	_, err := client.Rest.Request(http.MethodPatch, "/channels/"+channelID.String()+"/messages"+messageID.String(), content)
	return err
}

func (client Client) DeleteMessage(channelID Snowflake, messageID Snowflake) error {
	_, err := client.Rest.Request(http.MethodDelete, "/channels/"+channelID.String()+"/messages"+messageID.String(), nil)
	return err
}

func (client Client) CrosspostMessage(channelID Snowflake, messageID Snowflake) error {
	_, err := client.Rest.Request(http.MethodPost, "/channels/"+channelID.String()+"/messages"+messageID.String()+"/crosspost", nil)
	return err
}

func (client Client) FetchUser(id Snowflake) (User, error) {
	raw, err := client.Rest.Request(http.MethodGet, "/users/"+id.String(), nil)
	if err != nil {
		return User{}, err
	}

	res := User{}
	sonnet.Unmarshal(raw, &res)
	if err != nil {
		return User{}, errors.New("failed to parse received data from discord")
	}

	return res, nil
}

func (client Client) FetchMember(guildID Snowflake, memberID Snowflake) (Member, error) {
	raw, err := client.Rest.Request(http.MethodGet, "/guilds/"+guildID.String()+"/members/"+memberID.String(), nil)
	if err != nil {
		return Member{}, err
	}

	res := Member{}
	sonnet.Unmarshal(raw, &res)
	if err != nil {
		return Member{}, errors.New("failed to parse received data from discord")
	}

	return res, nil
}

func (client Client) RegisterCommand(command Command) error {
	if _, available := client.commands[command.Name]; available {
		return errors.New("client already has registered \"" + command.Name + "\" slash command (name already in use)")
	}

	tree := make(map[string]Command)
	tree["-"] = command
	client.commands[command.Name] = tree
	return nil
}

func (client Client) RegisterSubCommand(subCommand Command, rootCommandName string) error {
	if _, available := client.commands[rootCommandName]; !available {
		return errors.New("missing \"" + rootCommandName + "\" slash command in registry (root command needs to be registered in client before adding subcommands)")
	}

	if _, available := client.commands[rootCommandName][subCommand.Name]; available {
		return errors.New("client already has registered \"" + rootCommandName + "@" + subCommand.Name + "\" slash subcommand")
	}

	client.commands[rootCommandName][subCommand.Name] = subCommand
	return nil
}

// Sync currently cached slash commands to discord API. By default it'll try to make (bulk) global update (limit 100 updates per day), provide array with guild id snowflakes to update data only for specific guilds.
// You can also add second param -> slice with all command names you want to update (whitelist). There's also third, boolean param that when = true will reverse wishlist to work as blacklist.
func (client Client) SyncCommands(guildIDs []Snowflake, whitelist []string, switchMode bool) error {
	payload := parseCommandsToDiscordObjects(client.commands, whitelist, switchMode)

	if len(guildIDs) == 0 {
		_, err := client.Rest.Request(http.MethodPut, "/applications/"+client.ApplicationID.String()+"/commands", payload)
		return err
	}

	for _, guildID := range guildIDs {
		_, err := client.Rest.Request(http.MethodPut, "/applications/"+client.ApplicationID.String()+"/guilds/"+guildID.String()+"/commands", payload)
		if err != nil {
			return err
		}
	}

	return nil
}

// Starts bot on set route aka "endpoint". Setting example route = "/bot" and address = "192.168.0.7:9070" would make bot work under http://192.168.0.7:9070/bot.
// Set route as "/" or leave empty string to make it work on any URI (default).
func (client *Client) ListenAndServe(route string, address string) error {
	if client.running {
		panic("client's web server is already launched")
	}

	user, err := client.FetchUser(client.ApplicationID)
	if err != nil {
		panic("failed to fetch bot user's details (check if application id is correct & your internet connection works)\n")
	}
	client.User = user

	if route == "" {
		route = "/"
	}

	http.HandleFunc(route, client.handleDiscordWebhookRequests)
	return http.ListenAndServe(address, nil)
}

func (client *Client) ListenAndServeTLS(route string, address string, certFile, keyFile string) error {
	if client.running {
		panic("client's web server is already launched")
	}

	user, err := client.FetchUser(client.ApplicationID)
	if err != nil {
		panic("failed to fetch bot user's details (check if application id is correct & your internet connection works)\n")
	}
	client.User = user

	if route == "" {
		route = "/"
	}

	http.HandleFunc(route, client.handleDiscordWebhookRequests)
	return http.ListenAndServeTLS(address, certFile, keyFile, nil)
}

func CreateClient(options ClientOptions) Client {
	discordPublicKey, err := hex.DecodeString(options.PublicKey)
	if err != nil {
		panic("failed to decode \"%s\" discord's public key (check if it's correct key)")
	}

	client := Client{
		Rest:                       CreateRest(options.Token, options.GlobalRequestLimit, options.MaxRequestsBeforeSweep),
		ApplicationID:              options.ApplicationID,
		PublicKey:                  ed25519.PublicKey(discordPublicKey),
		commands:                   make(map[string]map[string]Command),
		queuedComponents:           make(map[string]*(chan *Interaction)),
		preCommandExecutionHandler: options.PreCommandExecutionHandler,
		interactionHandler:         options.InteractionHandler,
		running:                    false,
		cdrs:                       false,
	}

	if options.Cooldowns != nil {
		client.cdrs = true
		client.cdrDuration = options.Cooldowns.Duration
		client.cdrEphemeral = options.Cooldowns.Ephemeral
		client.cdrResponse = options.Cooldowns.CooldownResponse
		client.cdrCooldowns = make(map[Snowflake]time.Time, options.Cooldowns.MaxCooldownsBeforeSweep)
		client.cdrSinceSweep = 0

		if options.Cooldowns.MaxCooldownsBeforeSweep < 50 {
			client.cdrMaxBeforeSweep = 50
		} else {
			client.cdrMaxBeforeSweep = options.MaxRequestsBeforeSweep
		}
	}

	return client
}

func (client Client) handleDiscordWebhookRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	verified := verifyRequest(r, ed25519.PublicKey(client.PublicKey))
	if !verified {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var interaction Interaction
	err := sonnet.NewDecoder(r.Body).Decode(&interaction)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		panic(err) // Should never happen
	}
	defer r.Body.Close()

	interaction.Client = &client // Bind access to client instance which is needed for methods.
	switch interaction.Type {
	case PING_TYPE:
		w.Write([]byte(`{"type":1}`))
		return
	case APPLICATION_COMMAND_TYPE:
		command, interaction, available := client.getCommand(interaction)
		if !available {
			terminateCommandInteraction(w)
			return
		}

		if !command.AvailableInDM && interaction.GuildID == 0 {
			w.WriteHeader(http.StatusNoContent)
			return // Stop execution since this command doesn't want to be used inside DM.
		}

		if interaction.Member != nil && interaction.GuildID != 0 {
			interaction.Member.GuildID = interaction.GuildID // Bind guild id to each member so they can easily access guild CDN.
		}

		itx := CommandInteraction(interaction)
		if client.cdrs {
			var user User
			if interaction.GuildID == 0 {
				user = *itx.User
			} else {
				user = *itx.Member.User
			}

			now := time.Now()
			cooldown := client.cdrCooldowns[user.ID]
			timeLeft := cooldown.Sub(now)

			if timeLeft > 0 {
				if err := itx.SendReply(client.cdrResponse(user, timeLeft), client.cdrEphemeral); err != nil {
					panic("failed to send cooldown warning message to " + user.Tag() + ", original error: " + err.Error())
				}
				return
			} else {
				client.cdrSinceSweep++
				client.cdrCooldowns[user.ID] = time.Now().Add(client.cdrDuration)

				if client.cdrSinceSweep%client.cdrMaxBeforeSweep == 0 {
					client.cdrSinceSweep = 0

					go func() {
						for userID, cdr := range client.cdrCooldowns {
							if cdr.Sub(now) < 1 {
								delete(client.cdrCooldowns, userID)
							}
						}
					}()
				}
			}
		}

		if client.preCommandExecutionHandler != nil {
			content := client.preCommandExecutionHandler(itx)
			if content != nil {
				body, err := sonnet.Marshal(Response{
					Type: CHANNEL_MESSAGE_WITH_SOURCE_RESPONSE,
					Data: content,
				})

				if err != nil {
					panic("failed to parse payload received from client's \"pre command execution\" handler (make sure it's in JSON format)")
				}

				w.Header().Add("Content-Type", "application/json")
				w.Write(body)
				return
			}
		}

		w.WriteHeader(http.StatusNoContent)
		command.SlashCommandHandler(itx)
		return
	case MESSAGE_COMPONENT_TYPE:
		signalChannel, available := client.queuedComponents[interaction.Data.CustomID]
		if available && signalChannel != nil {
			*signalChannel <- &interaction
			acknowledgeComponentInteraction(w)
			return
		}

		if client.interactionHandler != nil {
			client.interactionHandler(interaction)
		}
		return
	case APPLICATION_COMMAND_AUTO_COMPLETE_TYPE:
		command, interaction, available := client.getCommand(interaction)
		if !available || command.AutoCompleteHandler == nil || len(command.Options) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		choices := command.AutoCompleteHandler(AutoCompleteInteraction(interaction))
		body, err := sonnet.Marshal(ResponseChoice{
			Type: AUTOCOMPLETE_RESPONSE,
			Data: ResponseChoiceData{
				Choices: choices,
			},
		})

		if err != nil {
			panic("failed to parse payload received from client's \"auto complete\" handler (make sure it's in JSON format)")
		}

		w.Header().Add("Content-Type", "application/json")
		w.Write(body)
		return
	default:
		if client.interactionHandler != nil {
			client.interactionHandler(interaction)
		}
	}
}

// Returns command, subcommand, a command context (updated interaction) and bool to check whether it succeeded and is safe to use.
func (client Client) getCommand(interaction Interaction) (Command, Interaction, bool) {
	if len(interaction.Data.Options) != 0 && interaction.Data.Options[0].Type == OPTION_SUB_COMMAND {
		rootName := interaction.Data.Name
		interaction.Data.Name, interaction.Data.Options = interaction.Data.Options[0].Name, interaction.Data.Options[0].Options
		command, available := client.commands[rootName][interaction.Data.Name]
		if !available {
			return Command{}, interaction, false
		}

		interaction.Data.Name = rootName + "@" + interaction.Data.Name
		return command, interaction, true
	}

	command, available := client.commands[interaction.Data.Name]["-"]
	if !available {
		return Command{}, interaction, false
	}

	return command, interaction, true
}
