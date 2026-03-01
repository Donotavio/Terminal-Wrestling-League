# agents.md - Terminal Wrestling League

## Fonte de verdade

Este repositório implementa o **Terminal Wrestling League**, um jogo PvP multiplayer via SSH com servidor autoritativo.

## Escopo funcional

- Cliente SSH envia input e recebe frames renderizados.
- Servidor mantém estado oficial da partida e resolve combate.
- Motor de combate deve ser **determinístico e reproduzível**.
- Replay deve reexecutar a luta com resultados idênticos.
- Ranking usa Glicko-2.
- Telemetria deve registrar métricas de combate, fila e comportamento.

## Restrições técnicas

- Linguagem: Go.
- Arquitetura modular.
- Engine de combate separado da renderização.
- Evitar dependências desnecessárias.
- Código de combate sem dependência de relógio de sistema.

## Estrutura obrigatória de pacotes

- `/cmd/server`
- `/internal/engine`
- `/internal/combat`
- `/internal/animation`
- `/internal/lobby`
- `/internal/matchmaking`
- `/internal/player`
- `/internal/npc`
- `/internal/ranking`
- `/internal/storage`
- `/internal/telemetry`
- `/internal/replay`

## Diretrizes de determinismo

- RNG com seed por partida.
- Ordem de resolução fixa por `player_id`.
- Fórmulas de combate em aritmética inteira (basis points).
- Sem uso de tempo real para decidir resultados de combate.
- Mesmos `seed + estado inicial + inputs` devem gerar saída idêntica.

## Qualidade de entrega

- Commits pequenos e objetivos.
- Testes unitários no motor de combate.
- Testes de determinismo/replay.
- Rodar `go test ./...` em cada etapa funcional.
- Rodar `go vet ./...` ao fechar um milestone.
