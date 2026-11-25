package store

import (
	"context"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

const (
	addJobMessageCommandType          = "AddJobMessage"
	addDeliveryObservationCommandType = "AddDeliveryObservation"
)

func (s *Store) AddJobMessage(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	commandID string,
	request domain.JobMessageRequest,
) (CommandReceipt, domain.JobMessage, error) {
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, commandID, addJobMessageCommandType, request,
		func(job domain.Job, now time.Time) (domain.JobMessage, error) {
			return domain.NewJobMessage(job, commandID, request, now)
		},
		func(job *domain.Job, message domain.JobMessage) {
			job.JobMessages = append(job.JobMessages, message)
		},
		func(job domain.Job) (domain.JobMessage, bool) {
			for _, message := range job.JobMessages {
				if message.CommandID == commandID {
					return message, true
				}
			}
			return domain.JobMessage{}, false
		},
	)
}

func (s *Store) AddDeliveryObservation(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	messageID, requestObservationID, commandID string,
	status domain.DeliveryStatus,
	evidenceSHA256 string,
	reconcilesUnknown bool,
) (CommandReceipt, domain.DeliveryObservation, error) {
	request := struct {
		MessageID         string                `json:"message_id"`
		CommandID         string                `json:"command_id"`
		RequestObservationID string                `json:"request_observation_id,omitempty"`
		Status                domain.DeliveryStatus `json:"status"`
		EvidenceSHA256    string                `json:"evidence_sha256,omitempty"`
		ReconcilesUnknown bool                  `json:"reconciles_unknown,omitempty"`
	}{
		MessageID: messageID, RequestObservationID: requestObservationID, CommandID: commandID, Status: status,
		EvidenceSHA256: evidenceSHA256, ReconcilesUnknown: reconcilesUnknown,
	}
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, commandID, addDeliveryObservationCommandType, request,
		func(job domain.Job, now time.Time) (domain.DeliveryObservation, error) {
			return domain.NewDeliveryObservation(job, messageID, requestObservationID, commandID, status, evidenceSHA256, reconcilesUnknown, now)
		},
		func(job *domain.Job, observation domain.DeliveryObservation) {
			job.DeliveryObservations = append(job.DeliveryObservations, observation)
		},
		func(job domain.Job) (domain.DeliveryObservation, bool) {
			for _, observation := range job.DeliveryObservations {
				if observation.CommandID == commandID {
					return observation, true
				}
			}
			return domain.DeliveryObservation{}, false
		},
	)
}
