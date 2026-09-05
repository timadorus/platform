// Package bus wires Watermill's NATS JetStream Publisher/Subscriber and owns the subject
// naming convention for the event bus: one subject per aggregate type
// (events_<aggregate_type>), never partitioned by aggregate id. Combined with the outbox
// relay's single-active-publisher design and each projection's serial consumer, this keeps
// per-aggregate ordering correct by construction (see docs/adr/0002).
package bus

import (
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"
	"github.com/ThreeDotsLabs/watermill/message"
)

// Subject returns the JetStream subject events for aggregateType are published to. Watermill's
// NATS JetStream integration auto-provisions a stream named after the raw topic string (not
// just the subject), and NATS stream names may not contain '.' (reserved for subject
// hierarchy) — so this uses '_' rather than the more conventional dotted "events.<type>"
// form.
func Subject(aggregateType string) string {
	return "events_" + aggregateType
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
		// A bare Nak() (the default when NakDelay is unset) redelivers essentially
		// immediately, giving the router's retry budget (projection.defaultMaxAttempts) far
		// less real wall-clock time than a cross-service event chain typically needs to
		// resolve — the outbox relay alone polls on a ~200ms interval (see docs/adr/0002).
		// A fixed per-attempt delay gives every projector's Nack/redelivery retry path (e.g.
		// CharacterProcessor.handleCharacterCreated waiting on CampaignProcessor's own async
		// seeding of a Campaign's default configuration) a real chance to converge instead of
		// exhausting retries and dead-lettering almost instantly. Confirmed via a real-cluster
		// e2e reproduction: with no delay, all 5 retries exhausted before the cross-service
		// chain could possibly complete, every single time.
		NakDelay: nats.NewStaticDelay(500 * time.Millisecond),
		JetStream: nats.JetStreamConfig{
			AutoProvision: true,
			DurablePrefix: durableName,
			// A durable JetStream consumer name is tied to a single filter subject, so if
			// a projector ever subscribes to more than one subject, each needs its own
			// durable name — otherwise the second Subscribe call would collide with the
			// first. Incorporating the topic keeps single-subject projectors (all of them
			// today) unaffected while making multi-subject projectors correct too.
			DurableCalculator: func(prefix, topic string) string {
				return prefix + "_" + topic
			},
		},
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("bus: new NATS subscriber: %w", err)
	}
	return sub, nil
}
