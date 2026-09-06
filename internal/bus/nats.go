// Package bus wires Watermill's NATS JetStream Publisher/Subscriber and owns the subject
// naming convention for the event bus: one subject per aggregate type
// (events_<aggregate_type>), never partitioned by aggregate id. Combined with the outbox
// relay's single-active-publisher design and each projection's serial consumer, this keeps
// per-aggregate ordering correct by construction (see docs/adr/0002).
package bus

import (
	"fmt"
	"strings"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"
	"github.com/ThreeDotsLabs/watermill/message"
)

// subjectPrefix is the one place the "events_" part of the subject naming convention is
// spelled out, so Subject and AggregateTypeFromSubject cannot drift apart.
const subjectPrefix = "events_"

// Subject returns the JetStream subject events for aggregateType are published to. Watermill's
// NATS JetStream integration auto-provisions a stream named after the raw topic string (not
// just the subject), and NATS stream names may not contain '.' (reserved for subject
// hierarchy) — so this uses '_' rather than the more conventional dotted "events.<type>"
// form.
func Subject(aggregateType string) string {
	return subjectPrefix + aggregateType
}

// AggregateTypeFromSubject is the literal inverse of Subject: it recovers the aggregate type a
// subject carries events for. It exists so a full read-model rebuild can compute a catch-up
// target per aggregate type — a projector's checkpoint only ever advances from events on its
// own subject, so its real target is the maximum global_seq among events of ITS aggregate type,
// not the whole-table maximum (see internal/rebuildreadmodels.ComputeTargets) — without any
// caller having to duplicate or guess the "events_<aggregate_type>" naming convention. Being
// the literal inverse of Subject, sharing subjectPrefix with it, it cannot drift out of sync.
//
// A subject that does not carry the prefix is returned unchanged, matching strings.TrimPrefix:
// every subject in this platform comes from Subject, so there is no such case to report on.
func AggregateTypeFromSubject(subject string) string {
	return strings.TrimPrefix(subject, subjectPrefix)
}

// DurableName computes the JetStream durable consumer name NewSubscriber assigns for a given
// processor name and subject — exported so tooling that needs to address the exact same durable
// consumer directly (cmd/rebuild-read-models, which deletes consumers to force a full replay —
// see internal/rebuildreadmodels) never has to duplicate or guess this naming convention.
func DurableName(processorName, subject string) string {
	return processorName + "_" + subject
}

// NewPublisher constructs a Watermill Publisher backed by NATS JetStream, auto-provisioning
// the stream if it doesn't exist yet. Used by the outbox relay (internal/outbox).
func NewPublisher(url string, logger watermill.LoggerAdapter) (message.Publisher, error) {
	pub, err := nats.NewPublisher(nats.PublisherConfig{
		URL:               url,
		Marshaler:         &nats.NATSMarshaler{},
		SubjectCalculator: nats.DefaultSubjectCalculator,
		JetStream: nats.JetStreamConfig{
			AutoProvision: true,
		},
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("bus: new NATS publisher: %w", err)
	}
	return pub, nil
}

// NewSubscriber constructs a Watermill Subscriber backed by a durable JetStream consumer.
// durableName should be stable across restarts (see internal/projection.Projector.Name) so
// JetStream resumes delivery from the last acknowledged message rather than replaying
// everything. Used by the projector (internal/projection).
func NewSubscriber(url, durableName string, logger watermill.LoggerAdapter) (message.Subscriber, error) {
	sub, err := nats.NewSubscriber(nats.SubscriberConfig{
		URL:              url,
		SubscribersCount: 1, // serial processing per projection, see docs/adr/0002
		Unmarshaler:      &nats.NATSMarshaler{},
		JetStream: nats.JetStreamConfig{
			AutoProvision: true,
			DurablePrefix: durableName,
			// A durable JetStream consumer name is tied to a single filter subject, so if
			// a projector ever subscribes to more than one subject, each needs its own
			// durable name — otherwise the second Subscribe call would collide with the
			// first. Incorporating the topic keeps single-subject projectors (all of them
			// today) unaffected while making multi-subject projectors correct too.
			DurableCalculator: DurableName,
		},
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("bus: new NATS subscriber: %w", err)
	}
	return sub, nil
}
