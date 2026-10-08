package app

import "errors"

// History references are not current membership. Bring only the configuration
// and DM structure needed to resolve a former member, never its other history
// and never a new membership. Removed source Bots remain inert provenance.
func closePortableHistory(b *portableBundle, bots []portableBot, convs []portableConversation) error {
	pool := map[string]portableBot{}
	dms := map[string]portableConversation{}
	have := map[string]bool{}
	need := map[string]bool{}
	for _, x := range bots {
		pool[x.ID] = x
	}
	for _, x := range convs {
		if x.Kind == "dm" {
			dms[x.ID] = x
		}
	}
	for _, x := range b.Bots {
		have[x.ID] = true
	}
	ref := func(id string) bool {
		if id == "" {
			return true
		}
		if b.Kind == "bot" && id != b.Bots[0].ID {
			return false
		}
		if _, ok := pool[id]; !ok {
			return false
		}
		need[id] = true
		return true
	}
	for i := range b.Messages {
		x := &b.Messages[i]
		if !ref(x.SenderBotID) {
			if x.Origin.SenderBotID == "" {
				x.Origin.SenderBotID = x.SenderBotID
			}
			x.SenderBotID = ""
		}
	}
	for i := range b.Memories {
		x := &b.Memories[i]
		if !ref(x.BotID) {
			if x.Origin.BotID == "" {
				x.Origin.BotID = x.BotID
			}
			x.BotID = ""
		}
	}
	for _, x := range b.Schedules {
		if !ref(x.BotID) {
			return errors.New("schedule Bot dependency is unavailable; select a different category")
		}
	}
	// Source order makes preview hashes and dependency resolution deterministic.
	for _, x := range bots {
		if !need[x.ID] || have[x.ID] {
			continue
		}
		dm, ok := dms[x.DMConversationID]
		if !ok {
			return errors.New("historical Bot DM dependency is unavailable")
		}
		b.Bots = append(b.Bots, x)
		b.Conversations = append(b.Conversations, dm)
		have[x.ID] = true
	}
	return nil
}
