<a id="readme-top"></a>

# Antigravity Account Switcher

[English](README.md) · Português (Brasil)

[![MIT](https://img.shields.io/github/license/koboshchan/antigravity-account-switcher)](LICENSE)
[![CI](https://github.com/koboshchan/antigravity-account-switcher/actions/workflows/ci.yml/badge.svg)](https://github.com/koboshchan/antigravity-account-switcher/actions/workflows/ci.yml)

Pool local de contas, painel de cotas em tempo real e proxy com alternância de contas em respostas HTTP 429 para Antigravity e `agy`.

## Sobre o projeto

O switcher supervisiona o Antigravity ou executa uma CLI com configurações de proxy restritas ao processo. Substitui a autorização enviada ao serviço pela conta selecionada no pool, consulta cotas no Google Cloud Code PA e alterna contas ao receber respostas compatíveis de esgotamento de cota. Requisições armazenadas em memória podem ser repetidas antes do envio da resposta ao cliente; um stream já iniciado não pode ser repetido de forma transparente.

O painel integrado mostra contas, cotas, uso de tokens e eventos do proxy. O fallback opcional tenta outro modelo antes de alternar de conta. Permissões e cotas do Google continuam determinando o acesso.

Desenvolvido com Go 1.24+, SQLite em Go puro (`modernc.org/sqlite`) e HTML, CSS e JavaScript incorporados. Não exige compilação com Node nem bibliotecas C em tempo de execução.

## Mudanças deste fork

Este fork acompanha o [projeto original de Muriel Gasparini](https://github.com/Muriel-Gasparini/antigravity-account-switcher).

O comando independente `refresh-quotas` agora utiliza a renovação de tokens OAuth, permitindo renovar tokens expirados antes de consultar cotas. Durante a migração, estados de erro antigos foram limpos somente após renovação e consulta bem-sucedidas. Foi um reparo local pontual, não uma redefinição automática de todas as contas com erro.

O proxy não substitui mais todos os modelos solicitados pelo modelo primário do painel. IDs no corpo, caminho e parâmetros de consulta são preservados, incluindo Argon, Hydrogen, Helium e IDs futuros. Testes de regressão cobrem esses casos. Isso não libera modelos ausentes do catálogo Google da conta.

O painel atualizado oferece temas claro e escuro com preferência salva. A descoberta de modelos prioriza o catálogo Google da conta e identifica a origem dos dados; listas de fallback são marcadas como não verificadas. É possível inserir um ID personalizado nos seletores. Nem a lista nem um ID personalizado garantem acesso ao modelo.

## Instalação

Use Linux ou WSL com Git, Make, Go 1.24 ou mais recente e uma instalação existente do Antigravity ou `agy`.

```sh
git clone https://github.com/koboshchan/antigravity-account-switcher.git
cd antigravity-account-switcher
make install
export PATH="$HOME/.local/bin:$PATH"
antigravity-account-switcher version
```

`make install` compila com `CGO_ENABLED=0` e instala em `~/.local/bin/antigravity-account-switcher`.

Adicione contas pelo OAuth no navegador, repetindo para cada conta do pool.

```sh
antigravity-account-switcher add-account
# Exibir a URL OAuth sem abrir o navegador automaticamente
antigravity-account-switcher add-account --no-browser
```

Na primeira inicialização com o pool vazio, credenciais ACP compatíveis podem ser importadas de `~/.gemini/antigravity-acp/acp_token.json` ou `~/.gemini/antigravity-cli/acp_token.json`. Não há comando geral de importação em lote do agy-swap.

## Uso

Inicie uma nova sessão supervisionada do Antigravity.

```sh
antigravity-account-switcher launch --open
# Informar o executável manualmente
antigravity-account-switcher launch --bin /path/to/antigravity
```

Para a CLI, use o executável real, não um wrapper de troca de contas. O caminho abaixo é apenas um exemplo; ajuste conforme sua instalação.

```sh
antigravity-account-switcher wrap -- ~/.local/libexec/agy
```

Execute apenas o proxy e o painel, sem iniciar o Antigravity.

```sh
antigravity-account-switcher serve --port 8080
```

Abra `http://127.0.0.1:8080`. A porta padrão é 8080, salvo configuração diferente. Escolha uma porta livre se outro supervisor estiver em execução. O comando `serve` sozinho não se conecta a um agente existente nem o reconfigura.

Consulte o pool ou sincronize cotas sem iniciar um agente.

```sh
antigravity-account-switcher list-accounts
antigravity-account-switcher refresh-quotas
antigravity-account-switcher status
antigravity-account-switcher config list
antigravity-account-switcher config set antigravity_bin /path/to/antigravity
```

Habilite o fallback de modelos explicitamente. O modelo primário serve de referência para o fallback, não substitui IDs recebidos nas requisições. Primário e secundário devem ser diferentes, mas podem pertencer ao mesmo provedor.

```sh
antigravity-account-switcher wrap --fallback-secondary \
  --model-primary gemini-3.8-flash-high \
  --model-secondary claude-sonnet-5-5 -- ~/.local/libexec/agy
```

Use IDs disponíveis para sua conta. Consulte `antigravity-account-switcher help` ou `--help` de cada comando. A configuração usa `config list`, `config get <key>` e `config set <key> <value>`.

## Armazenamento e segurança

O diretório padrão é `~/.config/antigravity-account-switcher/`, com `config.json` e `accounts.db`. Use `ANTIGRAVITY_CONFIG_DIR`, `ANTIGRAVITY_DB_PATH` ou a opção `--db` dos comandos compatíveis para selecionar outro armazenamento.

O banco SQLite contém credenciais OAuth. Mantenha-o privado; não publique bancos, tokens ou configurações com credenciais. Mantenha o painel restrito a localhost e use um túnel SSH para acesso remoto.

`launch` e `wrap` supervisionam o ciclo de vida do processo filho. Encerrar o supervisor pode encerrar esse processo. Não reinicie um supervisor durante um trabalho demorado apenas para atualizar o binário ou o painel.

## Contribuições

Abra uma [issue](https://github.com/koboshchan/antigravity-account-switcher/issues) ou envie um pull request com escopo definido para este fork. Leia [AGENTS.md](AGENTS.md) antes de alterar código.

```sh
make fmt
make lint
make test-race
make build-static
```

Os testes com detector de condições de corrida exigem um compilador C; os binários de distribuição não.

## Licença e créditos

Distribuído sob a [licença MIT](LICENSE). A licença original e o aviso de direitos autorais de Muriel Gasparini são preservados sem alterações.

Implementação original de [Muriel Gasparini](https://github.com/Muriel-Gasparini/antigravity-account-switcher). Fork mantido em [koboshchan/antigravity-account-switcher](https://github.com/koboshchan/antigravity-account-switcher).

Estrutura do README adaptada de [othneildrew/Best-README-Template](https://github.com/othneildrew/Best-README-Template/blob/master/BLANK_README.md). A licença MIT do template está disponível [aqui](https://github.com/othneildrew/Best-README-Template/blob/master/LICENSE.txt).

<p align="right"><a href="#readme-top">Voltar ao início</a></p>
