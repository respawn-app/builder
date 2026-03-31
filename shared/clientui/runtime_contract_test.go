package clientui

import (
	"reflect"
	"testing"
)

func TestRuntimeClientUsesBundledStatusReadModel(t *testing.T) {
	clientType := reflect.TypeOf((*RuntimeClient)(nil)).Elem()
	if _, ok := clientType.MethodByName("Status"); !ok {
		t.Fatal("expected RuntimeClient to expose bundled Status() read model")
	}
	if _, ok := clientType.MethodByName("SessionView"); !ok {
		t.Fatal("expected RuntimeClient to expose bundled SessionView() hydration model")
	}

	legacyReadMethods := []string{
		"ReviewerFrequency",
		"ReviewerEnabled",
		"AutoCompactionEnabled",
		"FastModeAvailable",
		"FastModeEnabled",
		"ConversationFreshness",
		"ParentSessionID",
		"LastCommittedAssistantFinalAnswer",
		"ThinkingLevel",
		"CompactionMode",
		"ContextUsage",
		"CompactionCount",
		"ChatSnapshot",
	}
	for _, methodName := range legacyReadMethods {
		if _, ok := clientType.MethodByName(methodName); ok {
			t.Fatalf("legacy read-only getter %s must stay out of RuntimeClient after Status() bundling", methodName)
		}
	}
}
