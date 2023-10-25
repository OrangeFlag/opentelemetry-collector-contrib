// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package windows // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/windows"

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
)

// EventXML is the rendered xml of an event.
type EventXML struct {
	EventID          EventID          `xml:"System>EventID"`
	Provider         Provider         `xml:"System>Provider"`
	Computer         string           `xml:"System>Computer"`
	Channel          string           `xml:"System>Channel"`
	RecordID         uint64           `xml:"System>EventRecordID"`
	TimeCreated      TimeCreated      `xml:"System>TimeCreated"`
	Message          string           `xml:"RenderingInfo>Message"`
	RenderedLevel    string           `xml:"RenderingInfo>Level"`
	Level            string           `xml:"System>Level"`
	RenderedTask     string           `xml:"RenderingInfo>Task"`
	Task             string           `xml:"System>Task"`
	RenderedOpcode   string           `xml:"RenderingInfo>Opcode"`
	Opcode           string           `xml:"System>Opcode"`
	RenderedKeywords []string         `xml:"RenderingInfo>Keywords>Keyword"`
	Keywords         []string         `xml:"System>Keywords"`
	Security         *Security        `xml:"System>Security"`
	Execution        *Execution       `xml:"System>Execution"`
	EventData        []EventDataEntry `xml:"EventData>Data"`
	UserData         *AnyXML          `xml:"UserData"`
}

// parseTimestamp will parse the timestamp of the event.
func (e *EventXML) parseTimestamp() time.Time {
	if timestamp, err := time.Parse(time.RFC3339Nano, e.TimeCreated.SystemTime); err == nil {
		return timestamp
	}
	return time.Now()
}

// parseRenderedSeverity will parse the severity of the event.
func (e *EventXML) parseRenderedSeverity() entry.Severity {
	switch e.RenderedLevel {
	case "":
		return e.parseSeverity()
	case "Critical":
		return entry.Fatal
	case "Error":
		return entry.Error
	case "Warning":
		return entry.Warn
	case "Information":
		return entry.Info
	default:
		return entry.Default
	}
}

// parseSeverity will parse the severity of the event when RenderingInfo is not populated
func (e *EventXML) parseSeverity() entry.Severity {
	switch e.Level {
	case "1":
		return entry.Fatal
	case "2":
		return entry.Error
	case "3":
		return entry.Warn
	case "4":
		return entry.Info
	default:
		return entry.Default
	}
}

// parseBody will parse a body from the event.
func (e *EventXML) parseBody() map[string]interface{} {
	message, details := e.parseMessage()

	level := e.RenderedLevel
	if level == "" {
		level = e.Level
	}

	task := e.RenderedTask
	if task == "" {
		task = e.Task
	}

	opcode := e.RenderedOpcode
	if opcode == "" {
		opcode = e.Opcode
	}

	keywords := e.RenderedKeywords
	if keywords == nil {
		keywords = e.Keywords
	}

	body := map[string]interface{}{
		"event_id": map[string]interface{}{
			"qualifiers": e.EventID.Qualifiers,
			"id":         e.EventID.ID,
		},
		"provider": map[string]interface{}{
			"name":         e.Provider.Name,
			"guid":         e.Provider.GUID,
			"event_source": e.Provider.EventSourceName,
		},
		"system_time": e.TimeCreated.SystemTime,
		"computer":    e.Computer,
		"channel":     e.Channel,
		"record_id":   e.RecordID,
		"level":       level,
		"message":     message,
		"task":        task,
		"opcode":      opcode,
		"keywords":    keywords,
		"event_data":  parseEventData(e.EventData),
	}

	if len(details) > 0 {
		body["details"] = details
	}

	if e.Security != nil && e.Security.UserID != "" {
		body["security"] = map[string]any{
			"user_id": e.Security.UserID,
		}
	}

	if e.Execution != nil {
		body["execution"] = e.Execution.asMap()
	}

	if e.UserData != nil {
		body["user_data"] = e.UserData.asMap()
	}

	return body
}

// parseMessage will attempt to parse a message into a message and details
func (e *EventXML) parseMessage() (string, map[string]interface{}) {
	switch e.Channel {
	case "Security":
		return parseSecurity(e.Message)
	default:
		return e.Message, nil
	}
}

// parse event data entries into a map[string]interface
// where the key is the Name attribute, and value is the element value
// entries without Name are ignored
// see: https://learn.microsoft.com/en-us/windows/win32/wes/eventschema-datafieldtype-complextype
func parseEventData(entries []EventDataEntry) map[string]interface{} {
	outputMap := make(map[string]interface{}, len(entries))

	for _, entry := range entries {
		if entry.Name != "" {
			outputMap[entry.Name] = entry.Value
		}
	}

	return outputMap
}

// unmarshalEventXML will unmarshal EventXML from xml bytes.
func unmarshalEventXML(bytes []byte) (EventXML, error) {
	var eventXML EventXML
	if err := xml.Unmarshal(bytes, &eventXML); err != nil {
		return EventXML{}, fmt.Errorf("failed to unmarshal xml bytes into event: %w (%s)", err, string(bytes))
	}
	return eventXML, nil
}

// EventID is the identifier of the event.
type EventID struct {
	Qualifiers uint16 `xml:"Qualifiers,attr"`
	ID         uint32 `xml:",chardata"`
}

// TimeCreated is the creation time of the event.
type TimeCreated struct {
	SystemTime string `xml:"SystemTime,attr"`
}

// Provider is the provider of the event.
type Provider struct {
	Name            string `xml:"Name,attr"`
	GUID            string `xml:"Guid,attr"`
	EventSourceName string `xml:"EventSourceName,attr"`
}

type EventDataEntry struct {
	Name  string `xml:"Name,attr"`
	Value string `xml:",chardata"`
}

type AnyXML struct {
	tag        string
	attributes map[string]string
	chardata   string
	children   []AnyXML
}

func (a *AnyXML) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	a.tag = start.Name.Local

	a.attributes = make(map[string]string, len(start.Attr))
	for _, attr := range start.Attr {
		a.attributes[attr.Name.Local] = attr.Value
	}

	for {
		// We'll iterate over every token,
		// adding children elements and character data as we continue.
		anyToken, err := d.Token()
		if err != nil {
			return fmt.Errorf("token: %w", err)
		}

		switch t := anyToken.(type) {
		case xml.StartElement:
			child := AnyXML{}
			err := d.DecodeElement(&child, &t)
			if err != nil {
				return fmt.Errorf("decode start element: %w", err)
			}
			a.children = append(a.children, child)
		case xml.EndElement:
			// End element means we've reached the end of parsing
			return nil
		case xml.CharData:
			// Strip leading/trailing spaces to ignore newlines and
			// indentation in formatted XML
			a.chardata += string(bytes.TrimSpace([]byte(t)))
		case xml.Comment: // ignore comments
		case xml.ProcInst: // ignore processing instructions
		case xml.Directive: // ignore directives
		default:
			return fmt.Errorf("unexpected token type %t", t)
		}
	}
}

func (a AnyXML) asMap() map[string]any {
	m := make(map[string]any, 4)

	m["tag"] = a.tag

	if len(a.attributes) > 0 {
		m["attributes"] = a.attributes
	}

	if len(a.chardata) > 0 {
		m["chardata"] = a.chardata
	}

	if len(a.children) > 0 {
		childMaps := make([]map[string]any, 0, len(a.children))
		for _, child := range a.children {
			childMaps = append(childMaps, child.asMap())
		}
		m["children"] = childMaps
	}

	return m
}

// Security contains info pertaining to the user triggering the event.
type Security struct {
	UserID string `xml:"UserID,attr"`
}

// Execution contains info pertaining to the process that triggered the event.
type Execution struct {
	// ProcessID and ThreadID are required on execution info
	ProcessID uint `xml:"ProcessID,attr"`
	ThreadID  uint `xml:"ThreadID,attr"`
	// These remaining fields are all optional for execution info
	ProcessorID   *uint `xml:"ProcessorID,attr"`
	SessionID     *uint `xml:"SessionID,attr"`
	KernelTime    *uint `xml:"KernelTime,attr"`
	UserTime      *uint `xml:"UserTime,attr"`
	ProcessorTime *uint `xml:"ProcessorTime,attr"`
}

func (e Execution) asMap() map[string]any {
	result := map[string]any{
		"process_id": e.ProcessID,
		"thread_id":  e.ThreadID,
	}

	if e.ProcessorID != nil {
		result["processor_id"] = *e.ProcessorID
	}

	if e.SessionID != nil {
		result["session_id"] = *e.SessionID
	}

	if e.KernelTime != nil {
		result["kernel_time"] = *e.KernelTime
	}

	if e.UserTime != nil {
		result["user_time"] = *e.UserTime
	}

	if e.ProcessorTime != nil {
		result["processor_time"] = *e.ProcessorTime
	}

	return result
}
