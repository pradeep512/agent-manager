// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package semconv

import "go.opentelemetry.io/otel/attribute"

// AgentAttrs holds the inputs needed to build an agent root span attribute set
// for the "agent" span kind. Nil/zero optional fields are omitted from the output.
//
// The contract uses Layer-1 gen_ai.* keys only (the observer's
// hasAgentAttributes() keys off gen_ai.operation.name = "invoke_agent" or a
// non-empty gen_ai.agent.name).
type AgentAttrs struct {
	// Name is the agent name (gen_ai.agent.name). Required.
	Name string
	// Description is the agent's human-readable description
	// (gen_ai.agent.description). Optional.
	Description string
	// Framework is the AI framework / vendor (gen_ai.system). Optional.
	Framework string
	// RequestModel is the model used by the agent (gen_ai.request.model). Optional.
	RequestModel string
	// SystemInstructions is the system prompt text
	// (gen_ai.system_instructions). Optional; redacted when content is off.
	SystemInstructions string
	// ConversationID is the conversation / session identifier
	// (gen_ai.conversation.id). Optional.
	ConversationID string
	// Tools is the JSON-encoded list of tools available to the agent
	// (gen_ai.agent.tools). Optional.
	Tools string
	// InputMessages is the JSON-encoded list of input messages
	// (gen_ai.input.messages). Optional.
	InputMessages string
	// OutputMessages is the JSON-encoded list of output messages
	// (gen_ai.output.messages). Optional; written at End.
	OutputMessages string
}

// AgentAttributes returns the complete attribute set for an agent root span.
// The span name must be "invoke_agent" and gen_ai.operation.name is always
// "invoke_agent".
//
// The returned slice follows the published contract order:
//  1. gen_ai.operation.name = "invoke_agent"  (required — observer discriminator)
//  2. gen_ai.agent.name                        (required)
//  3. gen_ai.agent.description                 (optional)
//  4. gen_ai.system                            (optional — framework chip)
//  5. gen_ai.request.model                     (optional)
//  6. gen_ai.system_instructions               (optional — redacted when content off)
//  7. gen_ai.conversation.id                   (optional)
//  8. gen_ai.agent.tools                       (optional)
//  9. gen_ai.input.messages                    (optional)
//  10. gen_ai.output.messages                  (optional — written at End)
func AgentAttributes(a AgentAttrs) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "invoke_agent"),
		attribute.String("gen_ai.agent.name", a.Name),
	}
	if a.Description != "" {
		attrs = append(attrs, attribute.String("gen_ai.agent.description", a.Description))
	}
	if a.Framework != "" {
		attrs = append(attrs, attribute.String("gen_ai.system", a.Framework))
	}
	if a.RequestModel != "" {
		attrs = append(attrs, attribute.String("gen_ai.request.model", a.RequestModel))
	}
	if a.SystemInstructions != "" {
		attrs = append(attrs, attribute.String("gen_ai.system_instructions", a.SystemInstructions))
	}
	if a.ConversationID != "" {
		attrs = append(attrs, attribute.String("gen_ai.conversation.id", a.ConversationID))
	}
	if a.Tools != "" {
		attrs = append(attrs, attribute.String("gen_ai.agent.tools", a.Tools))
	}
	if a.InputMessages != "" {
		attrs = append(attrs, attribute.String("gen_ai.input.messages", a.InputMessages))
	}
	if a.OutputMessages != "" {
		attrs = append(attrs, attribute.String("gen_ai.output.messages", a.OutputMessages))
	}
	return attrs
}
