package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
)

func (s *Store) exportPortable(ctx context.Context, instance string, selection portableSelection, kind string) (portableBundle, error) {
	if kind == "" {
		kind = "account"
	}
	if kind != "account" && kind != "bot" {
		return portableBundle{}, errors.New("invalid export kind")
	}
	if len(selection.Categories) == 0 {
		selection.Categories = portableCategories
	}
	cat, err := portableSet(selection.Categories, portableCategories)
	if err != nil || !cat["bot_config"] {
		return portableBundle{}, errors.New("Bot configuration is required")
	}
	if kind == "bot" && (len(selection.BotIDs) != 1 || cat["settings"]) {
		return portableBundle{}, errors.New("Bot export requires exactly one Bot and excludes account settings")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return portableBundle{}, err
	}
	defer tx.Rollback()
	b := portableBundle{Format: "tofi.bundle", Version: 1, Kind: kind, SourceInstance: instance, CreatedAt: now(), Included: selection.Categories, Excluded: portableExcluded, Bots: []portableBot{}, Conversations: []portableConversation{}, Messages: []portableMessage{}, Memories: []portableMemory{}, Schedules: []portableSchedule{}}
	if cat["attachments"] {
		b.Version = 2
		b.Excluded = portableExclusions(true)
	}
	origins := map[string]portableOrigin{}
	rows, err := tx.QueryContext(ctx, `SELECT kind,target_id,source_json FROM portability_provenance`)
	if err != nil {
		return b, err
	}
	for rows.Next() {
		var kind, id, raw string
		if err = rows.Scan(&kind, &id, &raw); err != nil {
			break
		}
		var o portableOrigin
		if err = json.Unmarshal([]byte(raw), &o); err != nil {
			break
		}
		origins[kind+":"+id] = o
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return b, err
	}
	origin := func(kind, id string) portableOrigin {
		if o, ok := origins[kind+":"+id]; ok {
			return o
		}
		return portableOrigin{InstanceID: instance, RecordID: id}
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,name,instructions,model,reasoning_effort,dm_conversation_id,created_at,archived FROM bots ORDER BY created_at,id`)
	if err != nil {
		return b, err
	}
	for rows.Next() {
		x, e := scanBot(rows)
		if e != nil {
			err = e
			break
		}
		b.Bots = append(b.Bots, portableBot{Bot: x, Origin: origin("bot", x.ID)})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return b, err
	}
	allBots := append([]portableBot(nil), b.Bots...)
	// Select before reading content so standalone exports never include group history.
	chosen := map[string]bool{}
	for _, id := range selection.BotIDs {
		if chosen[id] {
			return b, errors.New("duplicate Bot selection")
		}
		chosen[id] = true
	}
	all := len(selection.BotIDs) == 0
	found := map[string]bool{}
	selected := []portableBot{}
	for _, x := range b.Bots {
		if all || chosen[x.ID] {
			selected = append(selected, x)
			found[x.ID] = true
		}
	}
	for id := range chosen {
		if !found[id] {
			return b, errors.New("selected Bot is not in this account")
		}
	}
	b.Bots = selected
	rows, err = tx.QueryContext(ctx, `SELECT id,kind,name,bot_id,updated_at,archived,user_visible FROM conversations ORDER BY id`)
	if err != nil {
		return b, err
	}
	var conversations []Conversation
	for rows.Next() {
		x, e := scanConv(rows)
		if e != nil {
			err = e
			break
		}
		conversations = append(conversations, x)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return b, err
	}
	for _, c := range conversations {
		members := []string{}
		rows, err = tx.QueryContext(ctx, `SELECT bot_id FROM members WHERE conversation_id=? ORDER BY bot_id`, c.ID)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			members = append(members, id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return b, err
		}
		keep := true
		if c.Kind == "dm" {
			keep = found[c.BotID]
		} else {
			for _, id := range members {
				keep = keep && found[id]
			}
			if kind == "bot" {
				keep = false
			}
			if len(members) == 0 && !all {
				keep = false
			}
		}
		if !keep {
			if c.Kind == "group" {
				for _, id := range members {
					if found[id] {
						b.SkippedGroups++
						break
					}
				}
			}
			continue
		}
		b.Conversations = append(b.Conversations, portableConversation{ID: c.ID, Kind: c.Kind, Name: c.Name, BotID: c.BotID, BotIDs: members, UpdatedAt: c.UpdatedAt, Archived: c.Archived, UserVisible: c.UserVisible})
		var attachmentCount int
		if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM attachments WHERE conversation_id=?)+(SELECT COUNT(*) FROM portability_missing_assets WHERE conversation_id=?)`, c.ID, c.ID).Scan(&attachmentCount); err != nil {
			return b, err
		}
		b.AttachmentCount += attachmentCount
		if cat["chats"] {
			rows, err = tx.QueryContext(ctx, `SELECT id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at FROM messages WHERE conversation_id=? ORDER BY seq`, c.ID)
			if err != nil {
				return b, err
			}
			for rows.Next() {
				var m portableMessage
				var sender, run, notice sql.NullString
				if err = rows.Scan(&m.ID, &m.ConversationID, &m.Seq, &m.Role, &m.Kind, &sender, &run, &m.Content, &notice, &m.CreatedAt); err != nil {
					break
				}
				m.SenderBotID = sender.String
				m.Origin = origin("message", m.ID)
				if m.Origin.Kind == "" {
					m.Origin.Kind = m.Kind
				}
				if m.Origin.RunID == "" {
					m.Origin.RunID = run.String
				}
				if m.Origin.SenderBotID == "" {
					m.Origin.SenderBotID = sender.String
				}
				if len(m.Origin.Notice) == 0 && notice.Valid && json.Valid([]byte(notice.String)) {
					m.Origin.Notice = json.RawMessage(notice.String)
				}
				b.Messages = append(b.Messages, m)
				if len(b.Messages) > portableMaxRecords {
					err = errors.New("export exceeds record limit; select fewer Bots or categories")
					break
				}
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return b, err
			}
		}
		if cat["memories"] {
			rows, err = tx.QueryContext(ctx, `SELECT id,conversation_id,bot_id,content,title,description,revision,created_at,updated_at FROM memories WHERE conversation_id=? ORDER BY id`, c.ID)
			if err != nil {
				return b, err
			}
			for rows.Next() {
				x, e := scanMemory(rows)
				if e != nil {
					err = e
					break
				}
				b.Memories = append(b.Memories, portableMemory{Memory: x, Origin: origin("memory", x.ID)})
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return b, err
			}
		}
		if cat["schedules"] {
			rows, err = tx.QueryContext(ctx, `SELECT id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at FROM schedules WHERE conversation_id=? ORDER BY id`, c.ID)
			if err != nil {
				return b, err
			}
			for rows.Next() {
				x, e := scanSchedule(rows)
				if e != nil {
					err = e
					break
				}
				b.Schedules = append(b.Schedules, portableSchedule{ID: x.ID, ConversationID: x.ConversationID, BotID: x.BotID, Content: x.Content, Title: x.Title, Description: x.Description, CreatedBy: x.CreatedBy, Kind: x.Kind, Timezone: x.Timezone, NextAtUTC: x.NextAtUTC, IntervalSeconds: x.IntervalSeconds, DailyTime: x.DailyTime, Status: x.Status, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt, Origin: origin("schedule", x.ID)})
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return b, err
			}
		}
	}
	if cat["settings"] {
		x := &portableSettings{}
		if err = tx.QueryRowContext(ctx, `SELECT timezone FROM user_preferences WHERE id=1`).Scan(&x.Timezone); err != nil && err != sql.ErrNoRows {
			return b, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT model,reasoning_effort FROM model_settings WHERE id=1`).Scan(&x.Model, &x.ReasoningEffort); err != nil && err != sql.ErrNoRows {
			return b, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT model FROM dictation_settings WHERE id=1`).Scan(&x.DictationModel); err != nil && err != sql.ErrNoRows {
			return b, err
		}
		b.Settings = x
	}
	var assetSnapshot portableAttachmentSnapshot
	if cat["attachments"] {
		b.AttachmentCount = 0
		if assetSnapshot, err = snapshotPortableAttachments(ctx, tx, &b, origins); err != nil {
			return b, err
		}
	}
	availableConversations := []portableConversation{}
	for _, c := range conversations {
		if c.Kind == "dm" {
			availableConversations = append(availableConversations, portableConversation{ID: c.ID, Kind: c.Kind, Name: c.Name, BotID: c.BotID, BotIDs: []string{c.BotID}, UpdatedAt: c.UpdatedAt, Archived: c.Archived, UserVisible: c.UserVisible})
		}
	}
	if err = closePortableHistory(&b, allBots, availableConversations); err != nil {
		return b, err
	}
	sort.Strings(b.Included)
	if err = tx.Commit(); err != nil {
		return b, err
	}
	if cat["attachments"] {
		if err = s.exportPortableAttachmentBytes(ctx, &b, assetSnapshot); err != nil {
			return b, err
		}
	}
	b.Counts = b.counts()
	if err = b.validate(); err != nil {
		return b, err
	}
	data, err := json.Marshal(b)
	if err != nil {
		return b, err
	}
	if len(data) > portableMaxBytes {
		return b, errors.New("export exceeds 16 MiB; select fewer Bots or categories")
	}
	return b, nil
}
