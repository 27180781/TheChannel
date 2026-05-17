package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/icza/dyno"
	"github.com/redis/go-redis/v9"
)

var redisType = os.Getenv("REDIS_PROTOCOL")
var redisAddr = os.Getenv("REDIS_ADDR")
var redisPass = os.Getenv("REDIS_PASSWORD")
var rdb *redis.Client

type Message struct {
	ID        int          `json:"id" redis:"id"`
	Type      string       `json:"type" redis:"type"`
	Text      string       `json:"text" redis:"text"`
	Author    string       `json:"author" redis:"author"`
	AuthorId  string       `json:"authorId" redis:"authorId"`
	Timestamp time.Time    `json:"timestamp" redis:"timestamp"`
	LastEdit  time.Time    `json:"last_edit" redis:"last_edit"`
	File      FileResponse `json:"file" redis:"-"`
	Deleted   bool         `json:"deleted" redis:"deleted"`
	Views     int          `json:"views" redis:"views"`
	Reactions Reactions    `json:"reactions" redis:"reactions"`
	IsAds     bool         `json:"is_ads" redis:"is_ads"`
}

type User struct {
	ID           string                 `json:"id"`
	Username     string                 `json:"username"`
	Email        string                 `json:"email"`
	PublicName   string                 `json:"publicName"`
	GlobalRole   GlobalRole             `json:"globalRole,omitempty"`
	ChannelRoles map[string]ChannelRole `json:"channelRoles,omitempty"`
}

type PushMessage struct {
	Type string  `json:"type"`
	M    Message `json:"message"`
}

func init() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rdb = redis.NewClient(&redis.Options{
		Network:  redisType,
		Addr:     redisAddr,
		Password: redisPass,
		DB:       0,
	})

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Connection to db failed: %v \n", err)
	}

	log.Println("Connection to DB successful!")
}

func getMessageNextId(ctx context.Context, slug string) int {
	id, err := rdb.Incr(ctx, fmt.Sprintf("channel:%s:message:next_id", slug)).Result()
	if err != nil {
		log.Fatalf("Failed to get id: %v\n", err)
	}

	return int(id)
}

func setMessage(ctx context.Context, slug string, m *Message, isUpdate bool) error {
	messageKey := fmt.Sprintf("channel:%s:messages:%d", slug, m.ID)

	// Load per-channel settings for regex replace
	settings, err := dbGetSettings(ctx, slug)
	if err == nil {
		cfg := settings.ToConfig()
		for _, regex := range cfg.RegexReplace {
			if !strings.HasPrefix(m.Text, "[quote-embedded#]") {
				t := regex.Pattern.ReplaceAllString(m.Text, regex.Replace)
				m.Text = t
			}
		}
	}

	// Set message in hash
	if err := rdb.HSet(ctx, messageKey, m).Err(); err != nil {
		return err
	}

	// Add message timestamp to sorted set
	if !isUpdate {
		if err := rdb.ZAdd(ctx, fmt.Sprintf("channel:%s:m_times", slug), redis.Z{Score: float64(m.Timestamp.Unix()), Member: messageKey}).Err(); err != nil {
			return err
		}
	}

	pushType := "new-message"
	if isUpdate {
		pushType = "edit-message"
	}

	pushMessage := PushMessage{
		Type: pushType,
		M:    *m,
	}

	pushMessageData, _ := json.Marshal(pushMessage)
	rdb.Publish(ctx, fmt.Sprintf("events:%s", slug), pushMessageData)

	return nil
}

func setReaction(ctx context.Context, slug string, messageId int, emoji string, userId string) error {
	kay := fmt.Sprintf("channel:%s:message:%d:reactions", slug, messageId)
	userId = fmt.Sprintf("%v", userId)

	react := map[string]string{
		userId: emoji,
	}

	prevReact, err := rdb.HGet(ctx, kay, userId).Result()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("failed to get previous reaction: %v", err)
	}

	if prevReact == emoji {
		react = map[string]string{
			userId: "",
		}
	}

	if err := rdb.HSet(ctx, kay, react).Err(); err != nil {
		return err
	}

	r, err := funcGetSumReactions(ctx, slug, messageId)
	if err != nil {
		return err
	}

	if err := updateMessageReactions(ctx, slug, messageId, r); err != nil {
		return err
	}

	pushMessage := PushMessage{
		Type: "reaction",
		M: Message{
			ID:        messageId,
			Reactions: r,
		},
	}

	pushMessageData, _ := json.Marshal(pushMessage)
	rdb.Publish(ctx, fmt.Sprintf("events:%s", slug), pushMessageData)

	return nil
}

var getMessageRange = redis.NewScript(`
	local time_set_key = KEYS[1]
	local offset_key = KEYS[2]

	local required_length = tonumber(ARGV[1])
	local isAdmin = ARGV[2] == 'true'
	local countViews = ARGV[3] == 'true'
	local direction = ARGV[4] or 'desc'


	local start_index
	if direction == 'asc' then
	    start_index = redis.call('ZRANK', time_set_key, offset_key) or 0
	else
	    start_index = redis.call('ZREVRANK', time_set_key, offset_key) or 0
	end

	if start_index > 0 then
		start_index = start_index + 1
	end

	local messages = {}
	repeat
		local batch_size = required_length - #messages
		local stop_index = start_index + batch_size
		local message_ids

		if direction == 'asc' then
		 message_ids = redis.call('ZRANGE', time_set_key, start_index, stop_index)
		else
		 message_ids = redis.call('ZREVRANGE', time_set_key, start_index, stop_index)
		end

		if #message_ids == 0 then
			break
		end

		for i, message_key in ipairs(message_ids) do
			local message_data = redis.call('HGETALL', message_key)
			local message = {}

			for j = 1, #message_data, 2 do
				local key = message_data[j]
				local value = message_data[j+1]

				if key == 'id' then
					message[key] = tonumber(value)
                elseif key == 'views' then
					if countViews then
						message[key] = tonumber(value)
					else
						message[key] = 0
					end
				elseif key == 'deleted' then
					message[key] = value == '1'
				elseif key == 'author' then
				    if isAdmin then
				        message[key] = value
				    else
				        message[key] = "Anonymous"
				    end
				elseif key == 'authorId' then
					if isAdmin then
					   message[key] = value
                    else
                       message[key] = "Anonymous"
                    end
				elseif key == 'reactions' then
				    local success, parsedReactions = pcall(cjson.decode, value)
					if success then
						message[key] = parsedReactions
					else
						message[key] = {}
					end
				elseif key == 'is_ads' then
				    message[key] = value == '1'
				else
					message[key] = value
				end
			end

			if not message['deleted'] or isAdmin then
				table.insert(messages, message)
			end
		end

		start_index = start_index + batch_size

	until #messages >= required_length

	return cjson.encode(messages)
`)

func funcGetMessageRange(ctx context.Context, slug string, start, stop int64, isAdmin, countViews bool, direction string) ([]Message, error) {
	offsetKeyName := fmt.Sprintf("channel:%s:messages:%d", slug, start)
	res, err := getMessageRange.Run(ctx, rdb, []string{
		fmt.Sprintf("channel:%s:m_times", slug),
		offsetKeyName,
	}, []string{strconv.FormatInt(stop, 10), strconv.FormatBool(isAdmin), strconv.FormatBool(countViews), direction}).Result()
	if err != nil {
		return []Message{}, err
	}

	if res == "{}" {
		return []Message{}, nil
	}

	var messages []Message
	resStr, _ := dyno.GetString(res)
	if err := json.Unmarshal([]byte(resStr), &messages); err != nil {
		return []Message{}, err
	}

	return messages, nil
}

var sumMessageReactions = redis.NewScript(`
  local reactions = redis.call('HVALS', KEYS[1])
  local result = {}

   for _, reaction in ipairs(reactions) do
   if reaction ~= "" then
    if result[reaction] then
	  result[reaction] = result[reaction] + 1
    else
	  result[reaction] = 1
   end
    end
  end
  return cjson.encode(result)
`)

func funcGetSumReactions(ctx context.Context, slug string, messageId int) (Reactions, error) {
	res, err := sumMessageReactions.Run(ctx, rdb, []string{
		fmt.Sprintf("channel:%s:message:%d:reactions", slug, messageId),
	}).Result()
	if err != nil || res == nil || res == "{}" {
		return nil, err
	}

	var reactions Reactions
	resStr, _ := dyno.GetString(res)
	if err := json.Unmarshal([]byte(resStr), &reactions); err != nil {
		return nil, fmt.Errorf("failed to unmarshal reactions: %v", err)
	}

	return reactions, nil
}

func updateMessageReactions(ctx context.Context, slug string, messageId int, reactions Reactions) error {
	messageKey := fmt.Sprintf("channel:%s:messages:%d", slug, messageId)

	exists, err := rdb.Exists(ctx, messageKey).Result()
	if err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("message %d does not exist", messageId)
	}

	reactionsJSON, err := json.Marshal(reactions)
	if err != nil {
		return fmt.Errorf("failed to marshal reactions: %v", err)
	}

	if err := rdb.HSet(ctx, messageKey, "reactions", reactionsJSON).Err(); err != nil {
		return err
	}

	return nil
}

func funcDeleteMessage(ctx context.Context, slug string, id string) error {
	msgKey := fmt.Sprintf("channel:%s:messages:%s", slug, id)
	rdb.HSet(ctx, msgKey, "deleted", true)

	var m Message
	idInt, _ := strconv.Atoi(id)
	m.ID = idInt
	m.Deleted = true
	m.LastEdit = time.Now()
	m.Text = "*ההודעה נמחקה*"
	m.File = FileResponse{}

	pushMessage := PushMessage{
		Type: "delete-message",
		M:    m,
	}
	pushMessageData, _ := json.Marshal(pushMessage)
	rdb.Publish(ctx, fmt.Sprintf("events:%s", slug), pushMessageData)

	return nil
}

func addViewsToMessages(ctx context.Context, slug string, countViews bool, messages []Message) {
	if !countViews {
		return
	}
	for _, m := range messages {
		rdb.HIncrBy(ctx, fmt.Sprintf("channel:%s:messages:%d", slug, m.ID), "views", 1)
	}
}

// https://redis.io/docs/latest/operate/oss_and_stack/management/security/#string-escaping-and-nosql-injection
func addSubscription(slug, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "subscriptions"
	if slug != "" {
		key = fmt.Sprintf("channel:%s:subscriptions", slug)
	}
	_, err := rdb.SAdd(ctx, key, token).Result()
	if err != nil {
		return err
	}

	return nil
}

func getSubcriptionsList(slug string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "subscriptions"
	if slug != "" {
		key = fmt.Sprintf("channel:%s:subscriptions", slug)
	}
	subscriptionsSet, err := rdb.SMembers(ctx, key).Result()
	if err != nil {
		log.Printf("Failed to get subscriptions: %v\n", err)
		return []string{}, err
	}
	return subscriptionsSet, nil
}

func getChannelDetails(ctx context.Context, slug string) (map[string]string, error) {
	return rdb.HGetAll(ctx, fmt.Sprintf("channel:%s", slug)).Result()
}

func dbSetEmojisList(ctx context.Context, slug string, emojis []string) error {
	emojisJSON, err := json.Marshal(emojis)
	if err != nil {
		return fmt.Errorf("failed to marshal emojis: %v", err)
	}

	key := fmt.Sprintf("channel:%s:emojis:list", slug)
	if err := rdb.Set(ctx, key, emojisJSON, 0).Err(); err != nil {
		return fmt.Errorf("failed to set emojis in db: %v", err)
	}

	return nil
}

func dbGetEmojisList(ctx context.Context, slug string) ([]string, error) {
	key := fmt.Sprintf("channel:%s:emojis:list", slug)
	emojisJSON, err := rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to get emojis from db: %v", err)
	}

	var emojisList []string
	if err := json.Unmarshal([]byte(emojisJSON), &emojisList); err != nil {
		return nil, fmt.Errorf("failed to unmarshal emojis: %v", err)
	}

	return emojisList, nil
}

func dbSetUsersList(ctx context.Context, usersList []User) error {
	jsonUsersList, err := json.Marshal(usersList)
	if err != nil {
		return err
	}
	if err := rdb.Set(ctx, "users:list", jsonUsersList, 0).Err(); err != nil {
		return err
	}

	return nil
}

func dbGetUsersList(ctx context.Context) ([]User, error) {
	u, err := rdb.Get(ctx, "users:list").Result()
	if err != nil {
		if err == redis.Nil {
			return []User{}, nil
		}
		return nil, err
	}
	var usersList []User
	err = json.Unmarshal([]byte(u), &usersList)
	if err != nil {
		return nil, err
	}

	return usersList, nil
}

func dbSetSettings(ctx context.Context, slug string, settings *Settings) error {
	jsonSettings, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %v", err)
	}

	key := fmt.Sprintf("channel:%s:settings", slug)
	if err := rdb.Set(ctx, key, jsonSettings, 0).Err(); err != nil {
		return fmt.Errorf("failed to set settings in db: %v", err)
	}

	return nil
}

func dbGetSettings(ctx context.Context, slug string) (Settings, error) {
	key := fmt.Sprintf("channel:%s:settings", slug)
	settingsJSON, err := rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return Settings{}, nil
		}
		return nil, fmt.Errorf("failed to get settings from db: %v", err)
	}

	var settings Settings
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %v", err)
	}

	return settings, nil
}

// Global settings (FCM/VAPID) stored under global:settings
func dbSetGlobalSettings(ctx context.Context, settings *Settings) error {
	jsonSettings, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal global settings: %v", err)
	}

	if err := rdb.Set(ctx, "global:settings", jsonSettings, 0).Err(); err != nil {
		return fmt.Errorf("failed to set global settings in db: %v", err)
	}

	return nil
}

func dbGetGlobalSettings(ctx context.Context) (Settings, error) {
	settingsJSON, err := rdb.Get(ctx, "global:settings").Result()
	if err != nil {
		if err == redis.Nil {
			// Fallback to legacy settings:list key
			settingsJSON2, err2 := rdb.Get(ctx, "settings:list").Result()
			if err2 != nil {
				if err2 == redis.Nil {
					return Settings{}, nil
				}
				return nil, fmt.Errorf("failed to get global settings from db: %v", err2)
			}
			settingsJSON = settingsJSON2
		} else {
			return nil, fmt.Errorf("failed to get global settings from db: %v", err)
		}
	}

	var settings Settings
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal global settings: %v", err)
	}

	return settings, nil
}

func dbGetUsersAmount(ctx context.Context, slug string) (int64, error) {
	key := fmt.Sprintf("channel:%s:registered_emails", slug)
	amount, err := rdb.SCard(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get users amount: %v", err)
	}
	return amount, nil
}

func getReportNextID(ctx context.Context, slug string) (int64, error) {
	return rdb.Incr(ctx, fmt.Sprintf("channel:%s:report:next_id", slug)).Result()
}

func dbReportMessage(ctx context.Context, slug string, report *Report) error {
	id, err := getReportNextID(ctx, slug)
	if err != nil {
		return err
	}
	reportKey := fmt.Sprintf("channel:%s:report:%d", slug, id)
	report.Id = id

	if err := rdb.HSet(ctx, reportKey, report).Err(); err != nil {
		return err
	}

	reportsListKey := fmt.Sprintf("channel:%s:reports:list", slug)
	reportsOpenKey := fmt.Sprintf("channel:%s:reports:open", slug)

	if err := rdb.ZAdd(ctx, reportsListKey, redis.Z{Score: float64(report.CreatedAt.Unix()), Member: reportKey}).Err(); err != nil {
		return err
	}

	if err := rdb.ZAdd(ctx, reportsOpenKey, redis.Z{Score: float64(report.CreatedAt.Unix()), Member: reportKey}).Err(); err != nil {
		return err
	}

	return nil
}

var getReportsScript = redis.NewScript(`
	local status = ARGV[1]
	local limit = tonumber(ARGV[2])

	local reportsStatusKey
	if status == 'open' then
		reportsStatusKey = KEYS[2]
	elseif status == 'all' then
		reportsStatusKey = KEYS[1]
	elseif status == 'closed' then
		reportsStatusKey = KEYS[3]
	end

	local reports = redis.call('ZREVRANGE', reportsStatusKey, 0, limit - 1)

	local result = {}
	for _, reportKey in ipairs(reports) do
		local report = redis.call('HGETALL', reportKey)

		local reportTable = {}
		for i = 1, #report, 2 do
		  local key = report[i]
		  local value = report[i + 1]

		   if key == 'messageId' then
			 reportTable[key] = tonumber(value)
		   elseif key == 'closed' then
			 reportTable[key] = value == '1'
		   elseif key == 'id' then
			 reportTable[key] = tonumber(value)
		   else
		     reportTable[key] = value
		   end

		end
		table.insert(result, reportTable)
	end

	return cjson.encode(result)
`)

func dbGetReports(ctx context.Context, slug string, status ReportStatus) (Reports, error) {
	listKey := fmt.Sprintf("channel:%s:reports:list", slug)
	openKey := fmt.Sprintf("channel:%s:reports:open", slug)
	closedKey := fmt.Sprintf("channel:%s:reports:closed", slug)

	jsonReports, err := getReportsScript.Run(ctx, rdb, []string{listKey, openKey, closedKey}, []string{string(status), "100"}).Result()
	if err != nil {
		return nil, err
	}

	var reports Reports
	if jsonReports == nil || jsonReports == "{}" {
		return reports, nil
	}

	resStr, _ := dyno.GetString(jsonReports)
	if resStr == "" {
		return nil, nil
	}

	if err := json.Unmarshal([]byte(resStr), &reports); err != nil {
		return nil, err
	}

	return reports, nil
}

func dbSetReports(ctx context.Context, slug string, report *Report) error {
	reportKey := fmt.Sprintf("channel:%s:report:%d", slug, report.Id)
	openKey := fmt.Sprintf("channel:%s:reports:open", slug)
	closedKey := fmt.Sprintf("channel:%s:reports:closed", slug)

	switch report.Closed {
	case true:
		if err := rdb.ZRem(ctx, openKey, reportKey).Err(); err != nil {
			return err
		}
		if err := rdb.ZAdd(ctx, closedKey, redis.Z{Score: float64(report.UpdatedAt.Unix()), Member: reportKey}).Err(); err != nil {
			return err
		}
	case false:
		if err := rdb.ZRem(ctx, closedKey, reportKey).Err(); err != nil {
			return err
		}
		if err := rdb.ZAdd(ctx, openKey, redis.Z{Score: float64(report.UpdatedAt.Unix()), Member: reportKey}).Err(); err != nil {
			return err
		}
	}

	if err := rdb.HSet(ctx, reportKey, "closed", report.Closed, "updatedAt", report.UpdatedAt).Err(); err != nil {
		return err
	}

	return nil
}

func dbSavePeakSSEConnections(slug string, peak *PeakSSEConnections) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := fmt.Sprintf("channel:%s:peak_sse_connections", slug)
	rdb.HSet(ctx, key, "value", peak.Value, "timestamp", peak.Timestamp.Unix())
}

func dbGetPeakSSEConnections(ctx context.Context, slug string) (*PeakSSEConnections, error) {
	key := fmt.Sprintf("channel:%s:peak_sse_connections", slug)
	p, err := rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var peak PeakSSEConnections
	vel, _ := dyno.GetInteger(p["value"])
	timestamp, _ := dyno.GetInteger(p["timestamp"])

	peak.Value = vel
	peak.Timestamp = time.Unix(timestamp, 0)

	return &peak, nil
}

func dbSaveSSEStatistics(slug string, amount int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := fmt.Sprintf("channel:%s:sse_statistics:%d:%d", slug, time.Now().Month(), time.Now().Year())
	member := fmt.Sprintf("%d&%s", amount, time.Now().Format("02-01-2006 15:04"))

	rdb.ZAdd(ctx, key, redis.Z{Score: float64(time.Now().Unix()), Member: member})
}

func dbGetSSEStatistics(ctx context.Context, slug string, length int64) (*Statistics, error) {
	key := fmt.Sprintf("channel:%s:sse_statistics:%d:%d", slug, time.Now().Month(), time.Now().Year())
	result := &Statistics{
		Data:   []int64{},
		Labels: []string{},
	}

	r, err := rdb.ZRevRangeWithScores(ctx, key, 0, length-1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get SSE statistics: %v", err)
	}

	if len(r) == 0 {
		return result, nil
	}

	for _, item := range r {
		itemMember, _ := dyno.GetString(item.Member)
		p := strings.Split(itemMember, "&")
		if len(p) != 2 {
			continue
		}
		val, _ := dyno.GetInteger(p[0])
		result.Data = append(result.Data, val)
		result.Labels = append(result.Labels, p[1])
	}

	return result, nil
}

func dbSaveScheduledMessages(ctx context.Context, slug string, messages *[]Message) error {
	jsonMessages, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("failed to marshal scheduled messages: %v", err)
	}

	key := fmt.Sprintf("channel:%s:scheduled_messages:list", slug)
	if err := rdb.Set(ctx, key, jsonMessages, 0).Err(); err != nil {
		return fmt.Errorf("failed to set scheduled messages in db: %v", err)
	}

	return nil
}

func dbGetScheduledMessages(ctx context.Context, slug string) (*[]Message, error) {
	key := fmt.Sprintf("channel:%s:scheduled_messages:list", slug)
	messagesJSON, err := rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return &[]Message{}, nil
		}
		return nil, fmt.Errorf("failed to get scheduled messages from db: %v", err)
	}

	var messages []Message
	if err := json.Unmarshal([]byte(messagesJSON), &messages); err != nil {
		return nil, fmt.Errorf("failed to unmarshal scheduled messages: %v", err)
	}

	return &messages, nil
}

// Channel CRUD functions

func dbChannelExists(ctx context.Context, slug string) (bool, error) {
	n, err := rdb.Exists(ctx, fmt.Sprintf("channel:%s", slug)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func dbCreateChannel(ctx context.Context, channel *ChannelData) error {
	hashKey := fmt.Sprintf("channel:%s", channel.Slug)

	if err := rdb.HSet(ctx, hashKey,
		"slug", channel.Slug,
		"name", channel.Name,
		"description", channel.Description,
		"logoUrl", channel.LogoUrl,
		"ownerEmail", channel.OwnerEmail,
		"createdAt", channel.CreatedAt.Format(time.RFC3339),
		"contactUs", channel.ContactUs,
	).Err(); err != nil {
		return err
	}

	featuresJSON, err := json.Marshal(channel.Features)
	if err != nil {
		return fmt.Errorf("failed to marshal features: %v", err)
	}

	featuresKey := fmt.Sprintf("channel:%s:features", channel.Slug)
	if err := rdb.Set(ctx, featuresKey, featuresJSON, 0).Err(); err != nil {
		return err
	}

	if err := rdb.ZAdd(ctx, "channels:list", redis.Z{
		Score:  float64(channel.CreatedAt.Unix()),
		Member: channel.Slug,
	}).Err(); err != nil {
		return err
	}

	return nil
}

func dbGetChannel(ctx context.Context, slug string) (*ChannelData, error) {
	hashKey := fmt.Sprintf("channel:%s", slug)
	h, err := rdb.HGetAll(ctx, hashKey).Result()
	if err != nil {
		return nil, err
	}
	if len(h) == 0 {
		return nil, redis.Nil
	}

	channel := &ChannelData{
		Slug:        h["slug"],
		Name:        h["name"],
		Description: h["description"],
		LogoUrl:     h["logoUrl"],
		OwnerEmail:  h["ownerEmail"],
		ContactUs:   h["contactUs"],
	}
	if h["slug"] == "" {
		channel.Slug = slug
	}

	if t, err := time.Parse(time.RFC3339, h["createdAt"]); err == nil {
		channel.CreatedAt = t
	}

	// Load features
	featuresKey := fmt.Sprintf("channel:%s:features", slug)
	featuresJSON, err := rdb.Get(ctx, featuresKey).Result()
	if err == nil {
		var features ChannelFeatures
		if err := json.Unmarshal([]byte(featuresJSON), &features); err == nil {
			channel.Features = features
		}
	}

	return channel, nil
}

func dbListChannels(ctx context.Context) ([]*ChannelData, error) {
	slugs, err := rdb.ZRange(ctx, "channels:list", 0, -1).Result()
	if err != nil {
		return nil, err
	}

	var channels []*ChannelData
	for _, slug := range slugs {
		ch, err := dbGetChannel(ctx, slug)
		if err != nil {
			log.Printf("Failed to get channel %s: %v\n", slug, err)
			continue
		}
		channels = append(channels, ch)
	}

	return channels, nil
}

func dbDeleteChannel(ctx context.Context, slug string) error {
	// Remove from channels:list
	if err := rdb.ZRem(ctx, "channels:list", slug).Err(); err != nil {
		return err
	}

	// Scan and delete all channel:{slug}:* keys
	pattern := fmt.Sprintf("channel:%s:*", slug)
	var cursor uint64
	for {
		keys, nextCursor, err := rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := rdb.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// Delete the main channel hash
	if err := rdb.Del(ctx, fmt.Sprintf("channel:%s", slug)).Err(); err != nil {
		return err
	}

	return nil
}

func dbSetChannelFeatures(ctx context.Context, slug string, features *ChannelFeatures) error {
	featuresJSON, err := json.Marshal(features)
	if err != nil {
		return fmt.Errorf("failed to marshal features: %v", err)
	}

	featuresKey := fmt.Sprintf("channel:%s:features", slug)
	if err := rdb.Set(ctx, featuresKey, featuresJSON, 0).Err(); err != nil {
		return err
	}

	return nil
}

func dbAssignChannelRole(ctx context.Context, email, slug string, role ChannelRole) error {
	users, err := dbGetUsersList(ctx)
	if err != nil {
		return err
	}

	found := false
	for i, u := range users {
		if u.Email == email {
			if users[i].ChannelRoles == nil {
				users[i].ChannelRoles = make(map[string]ChannelRole)
			}
			users[i].ChannelRoles[slug] = role
			found = true
			break
		}
	}

	if !found {
		users = append(users, User{
			Email:        email,
			ChannelRoles: map[string]ChannelRole{slug: role},
		})
	}

	return dbSetUsersList(ctx, users)
}
