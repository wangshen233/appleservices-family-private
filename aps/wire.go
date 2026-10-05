package aps

import (
	"encoding/binary"
	"errors"
	"io"
)

const maxFrame = 1 << 20

type field struct {
	id    byte
	value []byte
}
type frame struct {
	command byte
	fields  []field
}

func (f frame) get(id byte) []byte {
	for _, v := range f.fields {
		if v.id == id {
			return v.value
		}
	}
	return nil
}

func writeFrame(w io.Writer, f frame) error {
	size := 0
	for _, value := range f.fields {
		if len(value.value) > 65535 {
			return errors.New("aps: field is too large")
		}
		size += 3 + len(value.value)
	}
	if size > maxFrame {
		return errors.New("aps: frame is too large")
	}
	data := make([]byte, 5, size+5)
	data[0] = f.command
	binary.BigEndian.PutUint32(data[1:], uint32(size))
	for _, value := range f.fields {
		data = append(data, value.id, byte(len(value.value)>>8), byte(len(value.value)))
		data = append(data, value.value...)
	}
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func readFrame(r io.Reader) (frame, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return frame{}, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > maxFrame {
		return frame{}, errors.New("aps: frame is too large")
	}
	body := make([]byte, int(length))
	if _, err := io.ReadFull(r, body); err != nil {
		return frame{}, err
	}
	result := frame{command: header[0]}
	for len(body) > 0 {
		if len(body) < 3 {
			return frame{}, errors.New("aps: truncated field header")
		}
		length := int(binary.BigEndian.Uint16(body[1:3]))
		if length > len(body)-3 {
			return frame{}, errors.New("aps: truncated field data")
		}
		result.fields = append(result.fields, field{body[0], body[3 : 3+length]})
		body = body[3+length:]
	}
	return result, nil
}
