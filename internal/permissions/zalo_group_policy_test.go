package permissions

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// MethodRole fails closed, so an unclassified method is denied to everyone —
// including the owner. That is the right default, but it means a newly added
// method is invisible until it is listed, and the symptom (a method that just
// never works, for anybody) reads like a routing bug rather than a policy gap.
// These tests pin the classification.

func TestZaloGroupWritesRequireOperator(t *testing.T) {
	writes := []string{
		protocol.MethodZaloGroupCreate,
		protocol.MethodZaloGroupAddMembers,
		protocol.MethodZaloGroupRemoveMembers,
		protocol.MethodZaloGroupInviteLink,
	}
	for _, method := range writes {
		t.Run(method, func(t *testing.T) {
			if got := MethodRole(method); got != RoleOperator {
				t.Errorf("MethodRole(%s) = %v, want %v", method, got, RoleOperator)
			}
			// A viewer must not be able to create groups on the firm's Zalo
			// account — these actions are visible to customers.
			if HasMinRole(RoleViewer, MethodRole(method)) {
				t.Errorf("%s must not be reachable by a viewer", method)
			}
		})
	}
}

func TestZaloServicesListIsReadOnly(t *testing.T) {
	if got := MethodRole(protocol.MethodZaloServicesList); got != RoleViewer {
		t.Errorf("MethodRole(%s) = %v, want %v",
			protocol.MethodZaloServicesList, got, RoleViewer)
	}
}

// Guards the fail-closed default itself: if this ever starts returning
// something other than RoleNone, every unclassified method silently opens up.
func TestUnclassifiedMethodDenied(t *testing.T) {
	if got := MethodRole("zalo.group.somethingNobodyAdded"); got != RoleNone {
		t.Errorf("unclassified method = %v, want %v (fail-closed)", got, RoleNone)
	}
}
