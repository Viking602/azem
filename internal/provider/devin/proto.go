package devin

import (
	"encoding/binary"
	"fmt"
	"math"
	"unicode/utf8"
)

// Field numbers follow the Devin CLI Cascade protocol, documented by
// can1357/oh-my-pi packages/catalog/src/discovery/devin-proto.ts.
// Decode only the required shapes, with one shared field budget per response.
const maxProtoFields = 65536

type proto []byte

func (p *proto) number(field int, value uint64) {
	if value == 0 {
		return
	}
	*p = binary.AppendUvarint(*p, uint64(field<<3))
	*p = binary.AppendUvarint(*p, value)
}

func (p *proto) data(field int, value []byte) {
	if len(value) == 0 {
		return
	}
	*p = binary.AppendUvarint(*p, uint64(field<<3|2))
	*p = binary.AppendUvarint(*p, uint64(len(value)))
	*p = append(*p, value...)
}

func (p *proto) text(field int, value string) { p.data(field, []byte(value)) }

func (p *proto) double(field int, value float64) {
	*p = binary.AppendUvarint(*p, uint64(field<<3|1))
	*p = binary.LittleEndian.AppendUint64(*p, math.Float64bits(value))
}

type protoField struct {
	number int
	wire   uint64
	value  uint64
	data   []byte
}
type protoMessage struct {
	fields []protoField
	budget *int
	err    error
}

func decodeProto(raw []byte, budget *int) *protoMessage {
	if budget == nil {
		count := maxProtoFields
		budget = &count
	}
	m := &protoMessage{budget: budget}
	for len(raw) > 0 && m.err == nil {
		if *budget <= 0 {
			m.err = fmt.Errorf("Devin protobuf field budget exceeded")
			break
		}
		*budget--
		tag, size := binary.Uvarint(raw)
		if size <= 0 || tag>>3 == 0 || tag>>3 > (1<<29)-1 {
			m.err = fmt.Errorf("invalid Devin protobuf tag")
			break
		}
		raw = raw[size:]
		field := protoField{number: int(tag >> 3), wire: tag & 7}
		switch field.wire {
		case 0:
			field.value, size = binary.Uvarint(raw)
			if size <= 0 {
				m.err = fmt.Errorf("invalid Devin protobuf integer")
				break
			}
			raw = raw[size:]
		case 1, 5:
			size = 8
			if field.wire == 5 {
				size = 4
			}
			if len(raw) < size {
				m.err = fmt.Errorf("truncated Devin protobuf number")
				break
			}
			field.data, raw = raw[:size], raw[size:]
		case 2:
			length, n := binary.Uvarint(raw)
			if n <= 0 || length > uint64(len(raw)-max(n, 0)) {
				m.err = fmt.Errorf("truncated Devin protobuf field")
				break
			}
			raw = raw[n:]
			field.data, raw = raw[:int(length)], raw[int(length):]
		default:
			m.err = fmt.Errorf("unsupported Devin protobuf wire type")
		}
		m.fields = append(m.fields, field)
	}
	return m
}

func (m *protoMessage) values(number int, wire uint64) []protoField {
	var result []protoField
	for _, f := range m.fields {
		if f.number != number {
			continue
		}
		if f.wire != wire {
			m.err = fmt.Errorf("invalid Devin protobuf type for field %d", number)
			continue
		}
		result = append(result, f)
	}
	return result
}

func (m *protoMessage) data(number int) []byte {
	fields := m.values(number, 2)
	if len(fields) == 0 {
		return nil
	}
	return fields[len(fields)-1].data
}

func (m *protoMessage) text(number int) string {
	raw := m.data(number)
	if !utf8.Valid(raw) {
		m.err = fmt.Errorf("invalid Devin protobuf UTF-8")
	}
	return string(raw)
}

func (m *protoMessage) number(number int) int {
	fields := m.values(number, 0)
	if len(fields) == 0 {
		return 0
	}
	n := fields[len(fields)-1].value
	if n > math.MaxInt32 {
		m.err = fmt.Errorf("Devin protobuf integer exceeds supported range")
		return 0
	}
	return int(n)
}

func (m *protoMessage) integer64(number int) int64 {
	fields := m.values(number, 0)
	if len(fields) == 0 {
		return 0
	}
	return int64(fields[len(fields)-1].value)
}

func (m *protoMessage) child(number int) *protoMessage {
	child := decodeProto(m.data(number), m.budget)
	if m.err != nil {
		child.err = m.err
	}
	return child
}
