# Orotools

CLI em Go (`oro`) para criação, inicialização e padronização de projetos de software.

## Install

```bash
curl -fsSL https://willsantos.github.io/orotools/install.sh | bash
```

O script detecta SO e arquitetura, valida o checksum sha256 e instala o binário
em `~/.local/bin` (root: `/usr/local/bin`).

Para instalar uma versão específica:

```bash
curl -fsSL https://willsantos.github.io/orotools/install.sh | bash -s -- --version v0.5.0
```

Atualizar uma instalação existente:

```bash
oro upgrade          # atualiza para a última release
oro upgrade --check  # apenas verifica se há versão nova
```

Todos os assets (tar.gz/zip para linux/darwin/windows, amd64/arm64, pacotes
`.deb`/`.rpm` e checksums) estão na
[página de releases](https://github.com/willsantos/orotools/releases).

### Build from source

Requer Go 1.25+.

```bash
go build ./cmd/oro
./oro --version
```

## Uso da CLI

### Quick start

Criar um projeto a partir de uma stack oficial:

```bash
oro new meu-app --stack fastify-next --yes
cd meu-app
oro info
```

Ver o plano sem executar nada:

```bash
oro new meu-app --stack dotnet-next --dry-run
```

Adotar um repositório existente (detecta stack e gera manifest):

```bash
cd projeto-existente
oro init
oro doctor --stack rails
```

### Comandos

| Comando | Descrição |
| ------- | --------- |
| `oro new [name]` | Cria projeto a partir de uma recipe |
| `oro apply` | Reconcilia o projeto com `orotools.yaml` e a recipe |
| `oro init` | Detecta a stack do diretório atual e gera `orotools.yaml` |
| `oro doctor` | Valida requirements (ferramentas/versões) da recipe |
| `oro info` | Mostra o estado do manifest do projeto |
| `oro add [skill\|agent\|pipeline] [value]` | Registra entrada no manifest |
| `oro recipe list` | Lista recipes oficiais embutidas |
| `oro dev` | Gerencia dev servers de projetos (`list`/`status`/`start`/`stop`/`logs`/`open`/`add`) |
| `oro upgrade` | Atualiza o `oro` para a última release |

Use `oro [comando] --help` para flags e detalhes de cada comando.

### Stacks oficiais

```bash
oro recipe list
```

| Stack | Descrição |
| ----- | --------- |
| `dotnet` | ASP.NET Core Web API, Worker ou Console |
| `dotnet-next` | Monorepo .NET Web API + Next.js |
| `fastify-next` | Monorepo Fastify API + Next.js |
| `rails` | Aplicação Ruby on Rails |

Recipe customizada (diretório com `recipe.yaml`):

```bash
oro new meu-app --recipe ./minha-recipe --yes
```

### `oro new` — flags principais

| Flag | Default | Descrição |
| ---- | ------- | --------- |
| `--stack` | — | Recipe oficial: `dotnet`, `dotnet-next`, `rails`, `fastify-next` |
| `--recipe` | — | Caminho para recipe customizada (alternativa ao `--stack`) |
| `--set key=value` | — | Define variável da recipe (repetível) |
| `--yes` | `false` | Modo não interativo (só flags + defaults) |
| `--dry-run` | `false` | Mostra o plano sem executar |
| `--repository` | `local` | Provider Git: `local`, `github`, `azure-devops` |
| `--pipeline` | derivado | CI: `none`, `github-actions`, `azure-pipelines` |
| `--ai` | `opencode` | Destino de skills/agents: `opencode`, `cursor`, `claude-code`, `codex`, `copilot` |
| `--visibility` | `private` | Visibilidade no GitHub: `private`, `public` |
| `--remote` | — | URL explícita do remote Git |
| `--organization` | — | URL da org Azure DevOps |
| `--azure-project` | — | Nome do projeto Azure DevOps |

Exemplo:

```bash
# Monorepo TS com variáveis explícitas
oro new loja --stack fastify-next --yes \
  --set database=postgres --set docker=true
```

Sem `--yes`, variáveis sem default são perguntadas no terminal (wizard interativo).

### Manifest (`orotools.yaml`)

Todo projeto gerado ou adotado mantém um manifest na raiz:

```bash
oro info              # lê orotools.yaml do diretório atual
oro apply             # reexecuta a recipe (idempotente)
oro apply --dry-run   # preview do reconcile
```

### Dev manager (`oro dev`)

Gerencia os dev servers de projetos a partir do `projects.config.json`
(`$CLIENTES_HOME`):

```bash
oro dev list             # tabela de projetos: ▶ rodando   ! porta ocupada   ○ parado
oro dev list clientes    # filtra pelo grupo (ignora caixa e acentos)
oro dev start api web    # inicia projetos em paralelo
oro dev stop api         # para o projeto (kill da árvore de processos)
```

## Estrutura

```
cmd/oro/        entrypoint (main)
internal/
  cli/          cobra root + comandos
  ui/           helpers de terminal (lipgloss)
  version/      formatação de versão
  recipe/       loader/validator de recipes
  planner/      recipe -> plano
  executor/     executa steps com idempotência
  ...           demais pacotes por subsistema
recipes/        recipes oficiais (dotnet, dotnet-next, rails, fastify-next)
docs/           landing page + install.sh (GitHub Pages)
```

## Licença

[MIT](./LICENSE).
