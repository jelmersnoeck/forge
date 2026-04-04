package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsEnvFile(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		path string
		want bool
	}{
		"bare .env":                  {path: ".env", want: true},
		"absolute .env":              {path: "/home/troy/.env", want: true},
		"nested .env":                {path: "/app/greendale/.env", want: true},
		".env.local":                 {path: ".env.local", want: true},
		".env.production":            {path: ".env.production", want: true},
		".env.development":           {path: "/app/.env.development", want: true},
		".env.test":                  {path: ".env.test", want: true},
		".env.staging":               {path: ".env.staging", want: true},
		".env.example":               {path: ".env.example", want: true},
		".env.sample":                {path: ".env.sample", want: true},
		".env.template":              {path: ".env.template", want: true},
		"regular go file":            {path: "main.go", want: false},
		"env in name but not dotenv": {path: "environment.go", want: false},
		"dotenv in directory name":   {path: "/app/.env-dir/config.yaml", want: false},
		"README":                     {path: "README.md", want: false},
		".envrc":                     {path: ".envrc", want: false},
		"hidden env-ish file":        {path: ".environment", want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r.Equal(tc.want, isEnvFile(tc.path), "isEnvFile(%q)", tc.path)
		})
	}
}

func TestCommandAccessesEnvFile(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		command string
		want    string // "" means no match
	}{
		"cat .env":               {command: "cat .env", want: ".env"},
		"cat /app/.env":          {command: "cat /app/.env", want: "/app/.env"},
		"source .env":            {command: "source .env", want: ".env"},
		"source .env.local":      {command: "source .env.local", want: ".env.local"},
		"cp .env.example .env":   {command: "cp .env.example .env", want: ".env.example"},
		"mv .env .env.bak":       {command: "mv .env .env.bak", want: ".env"},
		"head -n5 .env":          {command: "head -n5 .env", want: ".env"},
		"grep KEY .env":          {command: "grep KEY .env", want: ".env"},
		"echo foo > .env":        {command: "echo foo > .env", want: ".env"},
		"echo foo >> .env.local": {command: "echo foo >> .env.local", want: ".env.local"},
		"redirect to .env":       {command: "echo x >.env", want: ".env"},
		"pipe to .env":           {command: "something | tee .env", want: ".env"},
		"safe go build":          {command: "go build ./...", want: ""},
		"safe grep":              {command: "grep -r TODO .", want: ""},
		"safe echo":              {command: "echo hello world", want: ""},
		"env command (not file)": {command: "env VAR=val command", want: ""},
		"printenv":               {command: "printenv HOME", want: ""},
		"git diff":               {command: "git diff HEAD~1", want: ""},
		"dotenv in string":       {command: `echo "check .env docs"`, want: ""},
		"safe sed on other file": {command: "sed -i 's/foo/bar/' config.yaml", want: ""},
		"cat .envrc is allowed":  {command: "cat .envrc", want: ""},
		"cat .environment":       {command: "cat .environment", want: ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := commandAccessesEnvFile(tc.command)
			r.Equal(tc.want, got, "commandAccessesEnvFile(%q)", tc.command)
		})
	}
}

func TestShellTokenize(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		input string
		want  []string
	}{
		"simple":        {input: "cat .env", want: []string{"cat", ".env"}},
		"double quotes": {input: `echo "hello .env"`, want: []string{"echo", `"hello .env"`}},
		"single quotes": {input: `echo '.env'`, want: []string{"echo", `'.env'`}},
		"pipes":         {input: "cat .env | grep KEY", want: []string{"cat", ".env", "|", "grep", "KEY"}},
		"redirect":      {input: "echo foo >.env", want: []string{"echo", "foo", ">.env"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := shellTokenize(tc.input)
			r.Equal(tc.want, got)
		})
	}
}

func TestEnvFileError(t *testing.T) {
	r := require.New(t)
	result := envFileError(".env")
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, ".env")
	r.Contains(result.Content[0].Text, "blocked")
}
