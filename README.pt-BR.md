<a id="readme-top"></a>

# Antigravity Account Switcher

[English](README.md) · Português (Brasil)

[![MIT](https://img.shields.io/github/license/koboshchan/antigravity-account-switcher)](LICENSE)
[![CI](https://github.com/koboshchan/antigravity-account-switcher/actions/workflows/ci.yml/badge.svg)](https://github.com/koboshchan/antigravity-account-switcher/actions/workflows/ci.yml)

Pool local de contas, painel de cotas em tempo real e proxy com alternância de contas em respostas HTTP 429 para Antigravity e `agy`.

## Sobre o projeto

O switcher supervisiona o Antigravity ou executa uma CLI com configurações de proxy restritas ao processo. Substitui a autorização enviada ao serviço pela conta selecionada no pool, consulta cotas no Google Cloud Code PA e alterna contas ao receber respostas compatíveis de esgotamento de cota. Corpos de requisição são armazenados em memória até 1 GiB (1.073.741.824 bytes) por padrão; requisições grandes simultâneas podem consumir muita RAM. Requisições armazenadas em memória podem ser repetidas antes do envio da resposta ao cliente; um stream já iniciado não pode ser repetido de forma transparente.

O painel integrado mostra contas, cotas, uso de tokens e eventos do proxy. O proxy alterna contas, nunca modelos. Permissões e cotas do Google continuam determinando o acesso.

Desenvolvido com Go 1.24+, SQLite em Go puro (`modernc.org/sqlite`) e HTML, CSS e JavaScript incorporados. Não exige compilação com Node nem bibliotecas C em tempo de execução.

## Mudanças deste fork

Este fork acompanha o [projeto original de Muriel Gasparini](https://github.com/Muriel-Gasparini/antigravity-account-switcher).

O comando independente `refresh-quotas` agora utiliza a renovação de tokens OAuth, permitindo renovar tokens expirados antes de consultar cotas. Durante a migração, estados de erro antigos foram limpos somente após renovação e consulta bem-sucedidas. Foi um reparo local pontual, não uma redefinição automática de todas as contas com erro.

O proxy preserva o modelo solicitado, sem escolher um modelo primário ou secundário. IDs no corpo, caminho e parâmetros de consulta são preservados, incluindo Argon, Hydrogen, Helium e IDs futuros. Testes de regressão cobrem esses casos. Isso não libera modelos ausentes do catálogo Google da conta.

O painel oferece temas claro e escuro com preferência salva. As opções de modelo primário/secundário e fallback foram removidas do painel, da configuração e do fluxo de requisições. Campos antigos de roteamento de modelos são ignorados e não são gravados novamente ao salvar a configuração.

O roteamento de cotas é separado por família. Uma janela Claude/GPT esgotada não bloqueia requisições Gemini, nem o contrário. Todas as janelas aplicáveis precisam ter cota: uma janela de 5 horas cheia não supera uma cota semanal esgotada. Bloqueios por família são persistidos com horário de liberação e reconciliados após consultas de cota bem-sucedidas. Contas desativadas manualmente ou com erro de autenticação continuam excluídas. Argon tem uma família própria, separada dos demais modelos Gemini. A CLI e o painel mostram cotas Argon somente quando o Google retorna dados específicos; caso contrário, mostram desconhecido. Uma resposta HTTP 403 no endpoint de cotas torna a conta indisponível para todos os modelos e oculta seus números até uma consulta posterior bem-sucedida. A recuperação nunca remove uma desativação manual nem um erro de autenticação.

Cada família mantém sua própria conta ativa persistida. Cada requisição usa o token da conta daquela família; uma resposta 429 ou cota esgotada seleciona automaticamente a próxima conta elegível, sem alterar as outras famílias. Os comandos `status` e `refresh-quotas` e o painel identificam a conta selecionada por família. Requisições sem modelo ou com modelo desconhecido mantêm o comportamento legado de conta ativa.

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

Ao adicionar uma conta de outro computador, o redirecionamento Google pode apontar para o endereço de loopback desse computador, e não para o host do switcher. Copie a URL completa da barra de endereços para o campo de callback no diálogo pendente de adicionar conta. O switcher valida o login pendente e conclui a troca de tokens localmente; não acessa URLs arbitrárias coladas. Aguarde a confirmação de que a conta foi salva antes de fechar o diálogo.

Na primeira inicialização com o pool vazio, credenciais ACP compatíveis podem ser importadas de `~/.gemini/antigravity-acp/acp_token.json` ou `~/.gemini/antigravity-cli/acp_token.json`. Não há comando geral de importação em lote do agy-swap.

## Uso

Inicie uma nova sessão supervisionada do Antigravity.

```sh
antigravity-account-switcher launch --open
# Informar o executável manualmente
antigravity-account-switcher launch --bin /path/to/antigravity
# Escolher outra porta fixa para o painel
antigravity-account-switcher launch --dashboard-port 8081
```

Para a CLI, use o executável real, não um wrapper de troca de contas. O caminho abaixo é apenas um exemplo; ajuste conforme sua instalação.

```sh
antigravity-account-switcher wrap -- ~/.local/libexec/agy
```

Execute apenas o proxy e o painel, sem iniciar o Antigravity.

```sh
antigravity-account-switcher serve --port 8080
```

O painel escuta somente em `127.0.0.1` e usa a porta fixa 8080 por padrão, inclusive em `launch` e `wrap`. Use `--dashboard-port` com `launch` ou `wrap`, ou `--port` com `serve`, para substituir a porta salva. A prioridade é opção de linha de comando (o último alias prevalece), `ANTIGRAVITY_PORT`, configuração salva e, por fim, 8080. Se a porta estiver ocupada, o comando retorna um erro em vez de escolher outra porta. Abra `http://127.0.0.1:8080` ao usar o padrão. O comando `serve` sozinho não se conecta a um agente existente nem o reconfigura.

Consulte o pool ou sincronize cotas sem iniciar um agente.

```sh
antigravity-account-switcher list-accounts
antigravity-account-switcher refresh-quotas
antigravity-account-switcher status
antigravity-account-switcher config list
antigravity-account-switcher config set antigravity_bin /path/to/antigravity
```

Consulte `antigravity-account-switcher help` ou `--help` de cada comando. A configuração usa `config list`, `config get <key>` e `config set <key> <value>`.

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

### Verificação da conta Google

Quando o Google exige verificação, a conta fica excluída de todas as famílias de modelos, sem alterar a desativação manual ou erros de autenticação. Abra **Verify with Google** no cartão da conta, conclua a verificação e use **Refresh Quotas** ou execute `antigravity-account-switcher refresh-quotas`. A CLI também mostra o link validado do Google. Os links nunca são abertos automaticamente. Somente uma nova consulta direta de cotas ao Google bem-sucedida remove a exigência; cotas locais/em cache, horários de redefinição e outras requisições bem-sucedidas não a removem. A indisponibilidade por HTTP 403 da API de cotas continua sendo um estado separado.

A consulta de cotas se identifica como cliente Antigravity hub 2.23 para receber o bucket semanal independente do Argon. Se um servidor de linguagem local antigo omitir Argon, o switcher consulta o resumo direto de cotas. Argon permanece desconhecido quando o Google não informa seu próprio bucket; percentuais genéricos do Gemini nunca são usados em seu lugar.

Contas com consulta de cota ao vivo bem-sucedida que informa Gemini sem um grupo Argon separado recebem o selo Ultra. Argon e Carbon compartilham as cotas Gemini de 5 horas e semanais, incluindo o bloqueio por limite de uso. Consultas com falha não estabelecem essa inferência; grupos Argon separados continuam independentes.

Requisições Carbon usam apenas contas Ultra confirmadas com cota Gemini disponível. Sem conta elegível, o proxy retorna HTTP 429 sem tentar contas de Argon independente ou nível desconhecido. O roteamento Argon permanece inalterado.

Argon normal e Carbon exigem conta Ultra confirmada com cota Gemini disponível. Pedidos explícitos de `gemini-4-argon-trial` preferem Ultra e usam `gemini-4-argon-low` no upstream, com evento no painel. Contas não Ultra mantêm o ID trial. A resposta estruturada `403 SUBSCRIPTION_REQUIRED` exclui temporariamente a conta para o modelo rejeitado e tenta outra conta sem bloquear modelos diferentes. A exclusão dura 30 minutos no processo atual.

Quando nenhuma conta Ultra elegível permanece, solicitações Argon normais (base, low ou high) usam `gemini-4-argon-trial` em contas Pro com cota `gemini-argon-wtu-weekly` disponível. Carbon nunca usa esse fallback. Contas desativadas ou bloqueadas continuam excluídas.
