package pipe

import (
	"testing"
)

func TestToTopic(t *testing.T) {
	tests := []struct {
		name     string
		userName string
		topic    string
		want     string
	}{
		{
			name:     "basic topic",
			userName: "alice",
			topic:    "mytopic",
			want:     "alice/mytopic",
		},
		{
			name:     "already prefixed with username",
			userName: "alice",
			topic:    "alice/mytopic",
			want:     "alice/mytopic",
		},
		{
			name:     "different user prefix not stripped",
			userName: "alice",
			topic:    "bob/mytopic",
			want:     "alice/bob/mytopic",
		},
		{
			name:     "empty topic",
			userName: "alice",
			topic:    "",
			want:     "alice/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toTopic(tt.userName, tt.topic)
			if got != tt.want {
				t.Errorf("toTopic(%q, %q) = %q, want %q", tt.userName, tt.topic, got, tt.want)
			}
		})
	}
}

func TestToPublicTopic(t *testing.T) {
	tests := []struct {
		name  string
		topic string
		want  string
	}{
		{
			name:  "basic topic",
			topic: "mytopic",
			want:  "public/mytopic",
		},
		{
			name:  "already public prefixed",
			topic: "public/mytopic",
			want:  "public/mytopic",
		},
		{
			name:  "empty topic",
			topic: "",
			want:  "public/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toPublicTopic(tt.topic)
			if got != tt.want {
				t.Errorf("toPublicTopic(%q) = %q, want %q", tt.topic, got, tt.want)
			}
		})
	}
}

func TestParseArgList(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want []string
	}{
		{
			name: "single item",
			arg:  "alice",
			want: []string{"alice"},
		},
		{
			name: "multiple items",
			arg:  "alice,bob,charlie",
			want: []string{"alice", "bob", "charlie"},
		},
		{
			name: "items with spaces",
			arg:  "alice, bob , charlie",
			want: []string{"alice", "bob", "charlie"},
		},
		{
			name: "empty string",
			arg:  "",
			want: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseArgList(tt.arg)
			if len(got) != len(tt.want) {
				t.Errorf("parseArgList(%q) returned %d items, want %d", tt.arg, len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseArgList(%q)[%d] = %q, want %q", tt.arg, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestResolveTopic(t *testing.T) {
	tests := []struct {
		name   string
		input  TopicResolveInput
		expect TopicResolveOutput
	}{
		{
			name: "basic private topic",
			input: TopicResolveInput{
				UserName: "alice",
				Topic:    "mytopic",
				IsAdmin:  false,
				IsPublic: false,
			},
			expect: TopicResolveOutput{
				Name:        "alice/mytopic",
				WithoutUser: "mytopic",
			},
		},
		{
			name: "public topic",
			input: TopicResolveInput{
				UserName: "alice",
				Topic:    "mytopic",
				IsAdmin:  false,
				IsPublic: true,
			},
			expect: TopicResolveOutput{
				Name:        "public/mytopic",
				WithoutUser: "public/mytopic",
			},
		},
		{
			name: "admin with absolute path",
			input: TopicResolveInput{
				UserName: "admin",
				Topic:    "/rawtopic",
				IsAdmin:  true,
				IsPublic: false,
			},
			expect: TopicResolveOutput{
				Name:        "rawtopic",
				WithoutUser: "",
			},
		},
		{
			name: "admin without absolute path treated as regular user",
			input: TopicResolveInput{
				UserName: "admin",
				Topic:    "mytopic",
				IsAdmin:  true,
				IsPublic: false,
			},
			expect: TopicResolveOutput{
				Name:        "admin/mytopic",
				WithoutUser: "mytopic",
			},
		},
		{
			name: "topic already prefixed with username",
			input: TopicResolveInput{
				UserName: "alice",
				Topic:    "alice/mytopic",
				IsAdmin:  false,
				IsPublic: false,
			},
			expect: TopicResolveOutput{
				Name:        "alice/mytopic",
				WithoutUser: "alice/mytopic",
			},
		},
		{
			name: "public topic already prefixed",
			input: TopicResolveInput{
				UserName: "alice",
				Topic:    "public/mytopic",
				IsAdmin:  false,
				IsPublic: true,
			},
			expect: TopicResolveOutput{
				Name:        "public/mytopic",
				WithoutUser: "public/mytopic",
			},
		},
		{
			name: "access list exists - user has access",
			input: TopicResolveInput{
				UserName:           "bob",
				Topic:              "sharedtopic",
				IsAdmin:            false,
				IsPublic:           false,
				ExistingAccessList: []string{"bob", "charlie"},
				HasExistingAccess:  true,
				HasUserAccess:      true,
			},
			expect: TopicResolveOutput{
				Name:        "sharedtopic",
				WithoutUser: "sharedtopic",
			},
		},
		{
			name: "access list exists - user denied (private)",
			input: TopicResolveInput{
				UserName:           "eve",
				Topic:              "sharedtopic",
				IsAdmin:            false,
				IsPublic:           false,
				ExistingAccessList: []string{"bob", "charlie"},
				HasExistingAccess:  true,
				HasUserAccess:      false,
			},
			expect: TopicResolveOutput{
				Name:        "eve/sharedtopic",
				WithoutUser: "sharedtopic",
			},
		},
		{
			name: "access list exists - user denied (public) - generates new topic",
			input: TopicResolveInput{
				UserName:           "eve",
				Topic:              "sharedtopic",
				IsAdmin:            false,
				IsPublic:           true,
				ExistingAccessList: []string{"bob", "charlie"},
				HasExistingAccess:  true,
				HasUserAccess:      false,
			},
			expect: TopicResolveOutput{
				Name:             "public/sharedtopic",
				WithoutUser:      "public/sharedtopic",
				AccessDenied:     true,
				GenerateNewTopic: true,
			},
		},
		{
			name: "access list creator gets access",
			input: TopicResolveInput{
				UserName:           "alice",
				Topic:              "newtopic",
				IsAdmin:            false,
				IsPublic:           false,
				AccessList:         []string{"bob"},
				ExistingAccessList: []string{"bob"},
				HasExistingAccess:  true,
				IsAccessCreator:    true,
				HasUserAccess:      false,
			},
			expect: TopicResolveOutput{
				Name:        "newtopic",
				WithoutUser: "newtopic",
			},
		},
		{
			name: "admin bypasses access control",
			input: TopicResolveInput{
				UserName:           "admin",
				Topic:              "restricted",
				IsAdmin:            true,
				IsPublic:           false,
				ExistingAccessList: []string{"bob"},
				HasExistingAccess:  true,
				HasUserAccess:      false,
			},
			expect: TopicResolveOutput{
				Name:        "admin/restricted",
				WithoutUser: "restricted",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveTopic(tt.input)
			if got.Name != tt.expect.Name {
				t.Errorf("resolveTopic().Name = %q, want %q", got.Name, tt.expect.Name)
			}
			if got.WithoutUser != tt.expect.WithoutUser {
				t.Errorf("resolveTopic().WithoutUser = %q, want %q", got.WithoutUser, tt.expect.WithoutUser)
			}
			if got.AccessDenied != tt.expect.AccessDenied {
				t.Errorf("resolveTopic().AccessDenied = %v, want %v", got.AccessDenied, tt.expect.AccessDenied)
			}
			if got.GenerateNewTopic != tt.expect.GenerateNewTopic {
				t.Errorf("resolveTopic().GenerateNewTopic = %v, want %v", got.GenerateNewTopic, tt.expect.GenerateNewTopic)
			}
		})
	}
}
