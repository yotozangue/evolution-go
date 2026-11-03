package model

import "time"

// PollVote representa um voto em uma enquete do WhatsApp
type PollVote struct {
	ID              string    `json:"id"`
	CompanyID       string    `json:"companyId"`
	InstanceID      string    `json:"instanceId"`
	PollMessageID   string    `json:"pollMessageId"`
	PollChatJid     string    `json:"pollChatJid"`
	VoteMessageID   string    `json:"voteMessageId"`
	VoterJid        string    `json:"voterJid"`
	VoterPhone      string    `json:"voterPhone,omitempty"`
	VoterName       string    `json:"voterName,omitempty"`
	SelectedOptions []string  `json:"selectedOptions"` // SHA-256 hashes
	SelectedNames   []string  `json:"selectedNames,omitempty"`
	VotedAt         time.Time `json:"votedAt"`
	ReceivedAt      time.Time `json:"receivedAt"`
}

// PollResults representa os resultados agregados de uma enquete
type PollResults struct {
	PollMessageID string         `json:"pollMessageId"`
	PollChatJid   string         `json:"pollChatJid"`
	Question      string         `json:"question,omitempty"`
	TotalVotes    int            `json:"totalVotes"`
	Votes         []PollVote     `json:"votes"`
	OptionCounts  map[string]int `json:"optionCounts"` // hash -> count
	Options       []PollOption   `json:"options,omitempty"`
	Voters        []VoterInfo    `json:"voters"`
	Known         bool           `json:"-"` // enquete com opções salvas (enviada pela API ou vista no evento)
}

// VoterInfo representa informações de um votante
type VoterInfo struct {
	Jid             string    `json:"jid"`
	Phone           string    `json:"phone,omitempty"`
	Name            string    `json:"name,omitempty"`
	SelectedOptions []string  `json:"selectedOptions"`
	SelectedNames   []string  `json:"selectedNames,omitempty"`
	VotedAt         time.Time `json:"votedAt"`
}

// PollOption representa uma opção da enquete com o total de votos
type PollOption struct {
	Name  string `json:"name"`
	Hash  string `json:"hash"`
	Votes int    `json:"votes"`
}
