package command

import (
	"time"

	tempest "github.com/Amatsagu/Tempest"
)

var Swap tempest.Command = tempest.Command{
	Name:        "swap",
	Description: "Sends example embed and replaces it with plain text after 2 seconds.",
	SlashCommandHandler: func(itx *tempest.CommandInteraction) {
		itx.SendReply(tempest.ResponseMessageData{
			Content: "Example message",
			Embeds: []*tempest.Embed{
				{
					Title:       "Example embed title",
					Description: "Example embed description",
				},
			},
		}, false, nil)

		time.Sleep(time.Second * 2)

		itx.EditReply(tempest.ResponseMessageData{
			Content: "Modified hello message!",
			Embeds:  []*tempest.Embed{nil},
		}, false)
	},
}
