// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metric // import "go.opentelemetry.io/otel/sdk/export/metric"

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel/api/core"
	"go.opentelemetry.io/otel/api/metric"
)

const (
	// reserved ID for the noop label encoder
	noopLabelEncoderID int64 = 1 + iota
	// reserved ID for the default label encoder
	defaultLabelEncoderID

	// this must come last in enumeration
	lastLabelEncoderID
)

// labelEncoderIDCounter is for generating IDs for other label
// encoders.
var labelEncoderIDCounter int64 = lastLabelEncoderID

// NewLabelEncoderID returns a unique label encoder ID. It should be
// called once per each type of label encoder. Preferably in init() or
// in var definition.
func NewLabelEncoderID() int64 {
	return atomic.AddInt64(&labelEncoderIDCounter, 1)
}

// Batcher is responsible for deciding which kind of aggregation to
// use (via AggregationSelector), gathering exported results from the
// SDK during collection, and deciding over which dimensions to group
// the exported data.
//
// The SDK supports binding only one of these interfaces, as it has
// the sole responsibility of determining which Aggregator to use for
// each record.
//
// The embedded AggregationSelector interface is called (concurrently)
// in instrumentation context to select the appropriate Aggregator for
// an instrument.
//
// The `Process` method is called during collection in a
// single-threaded context from the SDK, after the aggregator is
// checkpointed, allowing the batcher to build the set of metrics
// currently being exported.
//
// The `CheckpointSet` method is called during collection in a
// single-threaded context from the Exporter, giving the exporter
// access to a producer for iterating over the complete checkpoint.
type Batcher interface {
	// AggregationSelector is responsible for selecting the
	// concrete type of Aggregator used for a metric in the SDK.
	//
	// This may be a static decision based on fields of the
	// Descriptor, or it could use an external configuration
	// source to customize the treatment of each metric
	// instrument.
	//
	// The result from AggregatorSelector.AggregatorFor should be
	// the same type for a given Descriptor or else nil.  The same
	// type should be returned for a given descriptor, because
	// Aggregators only know how to Merge with their own type.  If
	// the result is nil, the metric instrument will be disabled.
	//
	// Note that the SDK only calls AggregatorFor when new records
	// require an Aggregator. This does not provide a way to
	// disable metrics with active records.
	AggregationSelector

	// Process is called by the SDK once per internal record,
	// passing the export Record (a Descriptor, the corresponding
	// Labels, and the checkpointed Aggregator).
	//
	// The Context argument originates from the controller that
	// orchestrates collection.
	Process(ctx context.Context, record Record) error

	// CheckpointSet is the interface used by the controller to
	// access the fully aggregated checkpoint after collection.
	//
	// The returned CheckpointSet is passed to the Exporter.
	CheckpointSet() CheckpointSet

	// FinishedCollection informs the Batcher that a complete
	// collection round was completed.  Stateless batchers might
	// reset state in this method, for example.
	FinishedCollection()
}

// AggregationSelector supports selecting the kind of Aggregator to
// use at runtime for a specific metric instrument.
type AggregationSelector interface {
	// AggregatorFor returns the kind of aggregator suited to the
	// requested export.  Returning `nil` indicates to ignore this
	// metric instrument.  This must return a consistent type to
	// avoid confusion in later stages of the metrics export
	// process, i.e., when Merging multiple aggregators for a
	// specific instrument.
	//
	// Note: This is context-free because the aggregator should
	// not relate to the incoming context.  This call should not
	// block.
	AggregatorFor(*metric.Descriptor) Aggregator
}

// Aggregator implements a specific aggregation behavior, e.g., a
// behavior to track a sequence of updates to a counter, a measure, or
// an observer instrument.  For the most part, counter semantics are
// fixed and the provided implementation should be used.  Measure and
// observer metrics offer a wide range of potential tradeoffs and
// several implementations are provided.
//
// Aggregators are meant to compute the change (i.e., delta) in state
// from one checkpoint to the next, with the exception of LastValue
// aggregators.  LastValue aggregators are required to maintain the last
// value across checkpoints.
//
// Note that any Aggregator may be attached to any instrument--this is
// the result of the OpenTelemetry API/SDK separation.  It is possible
// to attach a counter aggregator to a Measure instrument (to compute
// a simple sum) or a LastValue aggregator to a measure instrument (to
// compute the last value).
type Aggregator interface {
	// Update receives a new measured value and incorporates it
	// into the aggregation.  Update() calls may arrive
	// concurrently as the SDK does not provide synchronization.
	//
	// Descriptor.NumberKind() should be consulted to determine
	// whether the provided number is an int64 or float64.
	//
	// The Context argument comes from user-level code and could be
	// inspected for distributed or span context.
	Update(context.Context, core.Number, *metric.Descriptor) error

	// Checkpoint is called during collection to finish one period
	// of aggregation by atomically saving the current value.
	// Checkpoint() is called concurrently with Update().
	// Checkpoint should reset the current state to the empty
	// state, in order to begin computing a new delta for the next
	// collection period.
	//
	// After the checkpoint is taken, the current value may be
	// accessed using by converting to one a suitable interface
	// types in the `aggregator` sub-package.
	//
	// The Context argument originates from the controller that
	// orchestrates collection.
	Checkpoint(context.Context, *metric.Descriptor)

	// Merge combines the checkpointed state from the argument
	// aggregator into this aggregator's checkpointed state.
	// Merge() is called in a single-threaded context, no locking
	// is required.
	Merge(Aggregator, *metric.Descriptor) error
}

// Exporter handles presentation of the checkpoint of aggregate
// metrics.  This is the final stage of a metrics export pipeline,
// where metric data are formatted for a specific system.
type Exporter interface {
	// Export is called immediately after completing a collection
	// pass in the SDK.
	//
	// The Context comes from the controller that initiated
	// collection.
	//
	// The CheckpointSet interface refers to the Batcher that just
	// completed collection.
	Export(context.Context, CheckpointSet) error
}

// LabelStorage provides an access to the ordered labels.
type LabelStorage interface {
	// NumLabels returns a number of labels in the storage.
	NumLabels() int
	// GetLabels gets a label from a passed index.
	GetLabel(int) core.KeyValue
}

// LabelSlice implements LabelStorage in terms of a slice.
type LabelSlice []core.KeyValue

var _ LabelStorage = LabelSlice{}

// NumLabels is a part of LabelStorage implementation.
func (s LabelSlice) NumLabels() int {
	return len(s)
}

// GetLabel is a part of LabelStorage implementation.
func (s LabelSlice) GetLabel(idx int) core.KeyValue {
	return s[idx]
}

// Iter returns an iterator going over the slice.
func (s LabelSlice) Iter() LabelIterator {
	return NewLabelIterator(s)
}

// LabelIterator allows iterating over an ordered set of labels. The
// typical use of the iterator is as follows:
//
//     iter := export.NewLabelIterator(getStorage())
//     for iter.Next() {
//       label := iter.Label()
//       // or, if we need an index:
//       // idx, label := iter.IndexedLabel()
//       // do something with label
//     }
type LabelIterator struct {
	storage LabelStorage
	idx     int
}

// NewLabelIterator creates an iterator going over a passed storage.
func NewLabelIterator(storage LabelStorage) LabelIterator {
	return LabelIterator{
		storage: storage,
		idx:     -1,
	}
}

// Next moves the iterator to the next label. Returns false if there
// are no more labels.
func (i *LabelIterator) Next() bool {
	i.idx++
	return i.idx < i.Len()
}

// Label returns current label. Must be called only after Next returns
// true.
func (i *LabelIterator) Label() core.KeyValue {
	return i.storage.GetLabel(i.idx)
}

// IndexedLabel returns current index and label. Must be called only
// after Next returns true.
func (i *LabelIterator) IndexedLabel() (int, core.KeyValue) {
	return i.idx, i.Label()
}

// Len returns a number of labels in the iterator's label storage.
func (i *LabelIterator) Len() int {
	return i.storage.NumLabels()
}

// Convenience function that creates a slice of labels from the passed
// iterator. The iterator is set up to start from the beginning before
// creating the slice.
func IteratorToSlice(iter LabelIterator) []core.KeyValue {
	l := iter.Len()
	if l == 0 {
		return nil
	}
	iter.idx = -1
	slice := make([]core.KeyValue, 0, l)
	for iter.Next() {
		slice = append(slice, iter.Label())
	}
	return slice
}

// LabelEncoder enables an optimization for export pipelines that use
// text to encode their label sets.
//
// This interface allows configuring the encoder used in the Batcher
// so that by the time the exporter is called, the same encoding may
// be used.
type LabelEncoder interface {
	// Encode is called (concurrently) in instrumentation context.
	//
	// The expectation is that when setting up an export pipeline
	// both the batcher and the exporter will use the same label
	// encoder to avoid the duplicate computation of the encoded
	// labels in the export path.
	Encode(LabelIterator) string

	// ID should return a unique positive number associated with
	// the label encoder. Stateless label encoders could return
	// the same number regardless of an instance, stateful label
	// encoders should return a number depending on their state.
	ID() int64
}

// CheckpointSet allows a controller to access a complete checkpoint of
// aggregated metrics from the Batcher.  This is passed to the
// Exporter which may then use ForEach to iterate over the collection
// of aggregated metrics.
type CheckpointSet interface {
	// ForEach iterates over aggregated checkpoints for all
	// metrics that were updated during the last collection
	// period. Each aggregated checkpoint returned by the
	// function parameter may return an error.
	// ForEach tolerates ErrNoData silently, as this is
	// expected from the Meter implementation. Any other kind
	// of error will immediately halt ForEach and return
	// the error to the caller.
	ForEach(func(Record) error) error
}

// Record contains the exported data for a single metric instrument
// and label set.
type Record struct {
	descriptor *metric.Descriptor
	labels     Labels
	aggregator Aggregator
}

// Labels stores complete information about a computed label set,
// including the labels in an appropriate order (as defined by the
// Batcher).  If the batcher does not re-order labels, they are
// presented in sorted order by the SDK.
type Labels interface {
	Iter() LabelIterator
	Encoded(LabelEncoder) string
}

type labels struct {
	encoderID int64
	encoded   string
	slice     LabelSlice
}

var _ Labels = &labels{}

// NewSimpleLabels builds a Labels object, consisting of an ordered
// set of labels in a provided slice and a unique encoded
// representation generated by the passed encoder.
func NewSimpleLabels(encoder LabelEncoder, kvs ...core.KeyValue) Labels {
	l := &labels{
		encoderID: encoder.ID(),
		slice:     kvs,
	}
	l.encoded = encoder.Encode(l.Iter())
	return l
}

// Iter is a part of an implementation of the Labels interface.
func (l *labels) Iter() LabelIterator {
	return l.slice.Iter()
}

// Encoded is a part of an implementation of the Labels interface.
func (l *labels) Encoded(encoder LabelEncoder) string {
	if l.encoderID == encoder.ID() {
		return l.encoded
	}
	return encoder.Encode(l.Iter())
}

// NewRecord allows Batcher implementations to construct export
// records.  The Descriptor, Labels, and Aggregator represent
// aggregate metric events received over a single collection period.
func NewRecord(descriptor *metric.Descriptor, labels Labels, aggregator Aggregator) Record {
	return Record{
		descriptor: descriptor,
		labels:     labels,
		aggregator: aggregator,
	}
}

// Aggregator returns the checkpointed aggregator. It is safe to
// access the checkpointed state without locking.
func (r Record) Aggregator() Aggregator {
	return r.aggregator
}

// Descriptor describes the metric instrument being exported.
func (r Record) Descriptor() *metric.Descriptor {
	return r.descriptor
}

// Labels describes the labels associated with the instrument and the
// aggregated data.
func (r Record) Labels() Labels {
	return r.labels
}
