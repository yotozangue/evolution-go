package poll_service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"time"

	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	"github.com/evolution-foundation/evolution-go/pkg/poll/model"
	"github.com/google/uuid"
	"github.com/lib/pq"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

// PollService define a interface para gerenciamento de votos de enquetes
type PollService interface {
	// SavePollVote salva um voto de enquete no banco de dados
	SavePollVote(ctx context.Context, vote *model.PollVote) error

	// GetPollResults retorna os resultados de uma enquete
	GetPollResults(ctx context.Context, pollMessageID string, instanceID string) (*model.PollResults, error)

	// SavePoll guarda pergunta e opções da enquete para traduzir os hashes dos votos
	SavePoll(ctx context.Context, instanceID, pollMessageID, chatJid, question string, options []string) error

	// OptionNames traduz hashes de opções para os nomes (hash desconhecido é ignorado)
	OptionNames(ctx context.Context, instanceID, pollMessageID string, hashes []string) []string
}

type pollService struct {
	db            *sql.DB
	loggerWrapper *logger_wrapper.LoggerManager
}

// NewPollService cria uma nova instância do serviço de polls
func NewPollService(db *sql.DB, loggerWrapper *logger_wrapper.LoggerManager) PollService {
	service := &pollService{
		db:            db,
		loggerWrapper: loggerWrapper,
	}

	// Auto-migration: criar tabela se não existir
	if err := service.autoMigrate(); err != nil {
		loggerWrapper.GetLogger("poll-service").LogError("[POLL] Auto-migration failed: %v", err)
	}

	return service
}

// autoMigrate cria a tabela poll_votes se não existir
func (s *pollService) autoMigrate() error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS poll_votes (
			id VARCHAR(255) PRIMARY KEY,
			company_id VARCHAR(255) NOT NULL,
			instance_id VARCHAR(255) NOT NULL,
			poll_message_id VARCHAR(255) NOT NULL,
			poll_chat_jid VARCHAR(255) NOT NULL,
			vote_message_id VARCHAR(255) NOT NULL,
			voter_jid VARCHAR(255) NOT NULL,
			voter_phone VARCHAR(255),
			voter_name VARCHAR(255),
			selected_options TEXT[] NOT NULL DEFAULT '{}',
			voted_at TIMESTAMP NOT NULL DEFAULT NOW(),
			received_at TIMESTAMP NOT NULL DEFAULT NOW(),
			CONSTRAINT unique_vote_per_poll UNIQUE (poll_message_id, voter_jid)
		);
		
		CREATE INDEX IF NOT EXISTS idx_poll_votes_company ON poll_votes(company_id);
		CREATE INDEX IF NOT EXISTS idx_poll_votes_instance ON poll_votes(instance_id);
		CREATE INDEX IF NOT EXISTS idx_poll_votes_poll_message ON poll_votes(poll_message_id);
		CREATE INDEX IF NOT EXISTS idx_poll_votes_chat ON poll_votes(poll_chat_jid);
		CREATE INDEX IF NOT EXISTS idx_poll_votes_voter ON poll_votes(voter_jid);

		CREATE TABLE IF NOT EXISTS poll_messages (
			instance_id VARCHAR(255) NOT NULL,
			poll_message_id VARCHAR(255) NOT NULL,
			poll_chat_jid VARCHAR(255) NOT NULL,
			question TEXT NOT NULL DEFAULT '',
			options TEXT[] NOT NULL DEFAULT '{}',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			PRIMARY KEY (instance_id, poll_message_id)
		);
	`

	s.loggerWrapper.GetLogger("poll-service").LogInfo("[POLL] Running auto-migration...")

	_, err := s.db.Exec(createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create poll_votes table: %w", err)
	}

	return nil
}

// SavePollVote salva um voto de enquete no banco de dados (NÃO-INVASIVO)
func (s *pollService) SavePollVote(ctx context.Context, vote *model.PollVote) error {
	// Log seguro - não expõe dados sensíveis
	s.loggerWrapper.GetLogger("poll-service").LogInfo("[POLL] Saving vote for poll %s from %s", vote.PollMessageID, vote.VoterJid)

	// Extrair telefone do JID de forma segura
	if vote.VoterPhone == "" && strings.Contains(vote.VoterJid, "@") {
		phone := strings.Split(vote.VoterJid, "@")[0]
		vote.VoterPhone = phone
	}

	// Garantir ID único
	if vote.ID == "" {
		vote.ID = uuid.New().String()
	}

	// Query INSERT com ON CONFLICT para evitar duplicatas (SEGURO)
	query := `
		INSERT INTO poll_votes (
			id, company_id, instance_id, poll_message_id, poll_chat_jid,
			vote_message_id, voter_jid, voter_phone, voter_name,
			selected_options, voted_at, received_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12
		)
		ON CONFLICT (poll_message_id, voter_jid)
		DO UPDATE SET
			selected_options = EXCLUDED.selected_options,
			voted_at = EXCLUDED.voted_at,
			received_at = EXCLUDED.received_at
	`

	_, err := s.db.ExecContext(ctx, query,
		vote.ID,
		vote.CompanyID,
		vote.InstanceID,
		vote.PollMessageID,
		vote.PollChatJid,
		vote.VoteMessageID,
		vote.VoterJid,
		vote.VoterPhone,
		vote.VoterName,
		pq.Array(vote.SelectedOptions),
		vote.VotedAt,
		vote.ReceivedAt,
	)

	if err != nil {
		s.loggerWrapper.GetLogger("poll-service").LogError("[POLL] Failed to save vote: %v", err)
		return fmt.Errorf("failed to save poll vote: %w", err)
	}

	s.loggerWrapper.GetLogger("poll-service").LogInfo("[POLL] Vote saved successfully for poll %s", vote.PollMessageID)
	return nil
}

// SavePoll guarda pergunta e opções da enquete (chamado no envio e ao ver a criação no evento)
func (s *pollService) SavePoll(ctx context.Context, instanceID, pollMessageID, chatJid, question string, options []string) error {
	query := `
		INSERT INTO poll_messages (instance_id, poll_message_id, poll_chat_jid, question, options)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (instance_id, poll_message_id) DO UPDATE SET
			poll_chat_jid = EXCLUDED.poll_chat_jid,
			question = EXCLUDED.question,
			options = EXCLUDED.options
	`
	if _, err := s.db.ExecContext(ctx, query, instanceID, pollMessageID, chatJid, question, pq.Array(options)); err != nil {
		return fmt.Errorf("failed to save poll: %w", err)
	}
	return nil
}

// getPoll retorna pergunta e opções salvas (ok=false se a enquete não é conhecida)
func (s *pollService) getPoll(ctx context.Context, instanceID, pollMessageID string) (question string, chatJid string, options []string, ok bool) {
	err := s.db.QueryRowContext(ctx,
		`SELECT question, poll_chat_jid, options FROM poll_messages WHERE instance_id = $1 AND poll_message_id = $2`,
		instanceID, pollMessageID,
	).Scan(&question, &chatJid, pq.Array(&options))
	if err != nil {
		if err != sql.ErrNoRows {
			s.loggerWrapper.GetLogger("poll-service").LogError("[POLL] Failed to load poll %s: %v", pollMessageID, err)
		}
		return "", "", nil, false
	}
	return question, chatJid, options, true
}

// OptionNames traduz hashes de opções para os nomes
func (s *pollService) OptionNames(ctx context.Context, instanceID, pollMessageID string, hashes []string) []string {
	_, _, options, ok := s.getPoll(ctx, instanceID, pollMessageID)
	if !ok {
		return nil
	}
	return namesForHashes(optionHashMap(options), hashes)
}

func optionHash(name string) string {
	sum := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%x", sum[:])
}

func optionHashMap(options []string) map[string]string {
	m := make(map[string]string, len(options))
	for _, option := range options {
		m[optionHash(option)] = option
	}
	return m
}

func namesForHashes(hashToName map[string]string, hashes []string) []string {
	names := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		if name, ok := hashToName[hash]; ok {
			names = append(names, name)
		}
	}
	return names
}

// GetPollResults retorna os resultados agregados de uma enquete
func (s *pollService) GetPollResults(ctx context.Context, pollMessageID string, instanceID string) (*model.PollResults, error) {
	s.loggerWrapper.GetLogger("poll-service").LogInfo("[POLL] Fetching results for poll %s", pollMessageID)

	question, pollChatJid, options, known := s.getPoll(ctx, instanceID, pollMessageID)
	hashToName := optionHashMap(options)

	query := `
		SELECT 
			id, company_id, instance_id, poll_message_id, poll_chat_jid,
			vote_message_id, voter_jid, voter_phone, voter_name,
			selected_options, voted_at, received_at
		FROM poll_votes
		WHERE poll_message_id = $1 AND instance_id = $2
		ORDER BY voted_at ASC
	`

	rows, err := s.db.QueryContext(ctx, query, pollMessageID, instanceID)
	if err != nil {
		s.loggerWrapper.GetLogger("poll-service").LogError("[POLL] Failed to query votes: %v", err)
		return nil, fmt.Errorf("failed to query poll votes: %w", err)
	}
	defer rows.Close()

	votes := []model.PollVote{}
	voters := []model.VoterInfo{}
	optionCounts := make(map[string]int)

	for rows.Next() {
		var vote model.PollVote
		var voterPhone, voterName sql.NullString

		err := rows.Scan(
			&vote.ID,
			&vote.CompanyID,
			&vote.InstanceID,
			&vote.PollMessageID,
			&vote.PollChatJid,
			&vote.VoteMessageID,
			&vote.VoterJid,
			&voterPhone,
			&voterName,
			pq.Array(&vote.SelectedOptions),
			&vote.VotedAt,
			&vote.ReceivedAt,
		)
		if err != nil {
			s.loggerWrapper.GetLogger("poll-service").LogError("[POLL] Failed to scan vote: %v", err)
			continue
		}
		vote.VoterPhone = voterPhone.String
		vote.VoterName = voterName.String

		// Voto retirado (nenhuma opção marcada) não conta
		if len(vote.SelectedOptions) == 0 {
			continue
		}

		vote.SelectedNames = namesForHashes(hashToName, vote.SelectedOptions)
		for _, option := range vote.SelectedOptions {
			optionCounts[option]++
		}

		votes = append(votes, vote)
		voters = append(voters, model.VoterInfo{
			Jid:             vote.VoterJid,
			Phone:           vote.VoterPhone,
			Name:            vote.VoterName,
			SelectedOptions: vote.SelectedOptions,
			SelectedNames:   vote.SelectedNames,
			VotedAt:         vote.VotedAt,
		})
	}

	if err = rows.Err(); err != nil {
		s.loggerWrapper.GetLogger("poll-service").LogError("[POLL] Rows iteration error: %v", err)
		return nil, fmt.Errorf("error iterating votes: %w", err)
	}

	if pollChatJid == "" && len(votes) > 0 {
		pollChatJid = votes[0].PollChatJid
	}

	results := &model.PollResults{
		PollMessageID: pollMessageID,
		PollChatJid:   pollChatJid,
		Question:      question,
		TotalVotes:    len(votes),
		Votes:         votes,
		OptionCounts:  optionCounts,
		Voters:        voters,
		Known:         known,
	}

	if known {
		results.Options = make([]model.PollOption, len(options))
		for i, option := range options {
			hash := optionHash(option)
			results.Options[i] = model.PollOption{Name: option, Hash: hash, Votes: optionCounts[hash]}
		}
	}

	s.loggerWrapper.GetLogger("poll-service").LogInfo("[POLL] Found %d votes for poll %s", len(votes), pollMessageID)
	return results, nil
}

// BuildPollVoteFromEvent constrói um model.PollVote a partir de eventos do WhatsApp (HELPER SEGURO)
// NOTA: Espera que voteInfo já tenha passado pelo JID swap (Sender = número real)
func BuildPollVoteFromEvent(
	pollInfo *types.MessageInfo,
	voteInfo *types.MessageInfo,
	decryptedVote *waProto.PollVoteMessage,
	companyID string,
	instanceID string,
) *model.PollVote {
	// Extrair opções selecionadas (hashes SHA-256)
	selectedOptions := make([]string, len(decryptedVote.SelectedOptions))
	for i, option := range decryptedVote.SelectedOptions {
		selectedOptions[i] = fmt.Sprintf("%x", option) // Converte bytes para hex
	}

	// Extrair telefone do votante
	// NOTA: O JID swap já foi feito antes de chegar aqui!
	// Se havia LID+WhatsApp, o Sender JÁ É o número real (@s.whatsapp.net) e SenderAlt é o LID
	voterPhone := voteInfo.Sender.User

	return &model.PollVote{
		ID:              uuid.New().String(),
		CompanyID:       companyID,
		InstanceID:      instanceID,
		PollMessageID:   pollInfo.ID,
		PollChatJid:     pollInfo.Chat.String(),
		VoteMessageID:   voteInfo.ID,
		VoterJid:        voteInfo.Sender.String(),
		VoterPhone:      voterPhone,
		VoterName:       voteInfo.PushName,
		SelectedOptions: selectedOptions,
		VotedAt:         voteInfo.Timestamp,
		ReceivedAt:      time.Now(),
	}
}
