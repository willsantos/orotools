package devmgr

import "testing"

func TestGroupKey(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Clientes", "clientes"},
		{"clientes", "clientes"},
		{"CLIENTES", "clientes"},
		{"  Clientes  ", "clientes"},
		{"Configurações", "configuracoes"},
		{"configuracoes", "configuracoes"},
		{"Ações", "acoes"},
		{"SaaS", "saas"},
		{"", ""},
		{"   ", ""},
		{"Ênfase", "enfase"},
		{"créditos", "creditos"},
		{"nao-acentuado-123", "nao-acentuado-123"},
		// Fold abrangente via NFD + strip Mn (review PR#197): qualquer
		// letra com decomposicao canônica dobra, não só as pt-BR.
		{"ção", "cao"},
		{"škoda", "skoda"},
		{"ğül", "gul"},
		{"ąędź", "aedz"},
		{"Łódź", "lodz"},
		{"ción", "cion"},
		{"für", "fur"},
		// Letras sem decomposição canônica (traço/ligadura) têm fold
		// explícito na tabela suplementar.
		{"øresund", "oresund"},
		{"đakovo", "dakovo"},
		{"straße", "strasse"},
		{"Æon", "aeon"},
		// Runas fora do Latim passam inalteradas (sem marcas a remover).
		{"日本語", "日本語"},
	}
	for _, c := range cases {
		if got := GroupKey(c.in); got != c.want {
			t.Errorf("GroupKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// groupsConfig builds a Config by hand (insertion order = slice order).
func groupsConfig(groups ...string) *Config {
	cfg := &Config{}
	for i, g := range groups {
		cfg.Add(string(rune('a'+i)), &Project{Group: g})
	}
	return cfg
}

func TestGroupsDerivedInFirstAppearanceOrder(t *testing.T) {
	cfg := groupsConfig("Clientes", "", "SaaS", "Prospects", "  ", "Clientes")
	got := cfg.Groups()
	want := []string{"Clientes", "SaaS", "Prospects"}
	if len(got) != len(want) {
		t.Fatalf("Groups() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Groups() = %v, want %v", got, want)
		}
	}
}

func TestGroupsDedupeCaseAccentKeepsFirstSpelling(t *testing.T) {
	cfg := groupsConfig("Configurações", "CLIENTES", "configuracoes", "clientes")
	got := cfg.Groups()
	if len(got) != 2 {
		t.Fatalf("Groups() = %v, want 2 grupos (dedupe por caixa/acento)", got)
	}
	if got[0] != "Configurações" || got[1] != "CLIENTES" {
		t.Fatalf("Groups() = %v, display deve ser a primeira grafia de cada", got)
	}
}

func TestGroupsEmptyWhenNoProjectHasGroup(t *testing.T) {
	cfg := groupsConfig("", "   ")
	if got := cfg.Groups(); len(got) != 0 {
		t.Fatalf("Groups() = %v, want vazio", got)
	}
	if got := (&Config{}).Groups(); len(got) != 0 {
		t.Fatalf("Groups() de config vazio = %v, want vazio", got)
	}
}
