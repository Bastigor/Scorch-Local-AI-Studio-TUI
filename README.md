# Scorch Local AI Studio

Interface de terminal (TUI) para conversar com modelos de linguagem e gerar imagens **localmente**, sem enviar nada para a nuvem.

## Como usar (Windows, sem compilar)

1. Baixe os dois arquivos da pasta [`dist/`](dist) e deixe-os na mesma pasta:
   - `Scorch-Setup.exe`
   - `Scorch.exe`
2. Execute `Scorch-Setup.exe` uma única vez. Ele detecta sua placa de vídeo e baixa o **motor de IA** para a pasta `studio-data\bin`:
   - `llama-server` (llama.cpp), que executa os modelos de linguagem;
   - `sd` (stable-diffusion.cpp), que gera as imagens.
3. Copie seus modelos para as pastas abaixo (ou use `i` na aba Modelos para importar um arquivo):
   - `studio-data\models\llm` — modelos de linguagem `.gguf`
   - `studio-data\models\checkpoints` — checkpoints de imagem `.safetensors` / `.ckpt`
4. Abra `Scorch.exe` (ou aceite a pergunta no final do Setup).

O aplicativo é só a interface: sem o motor instalado pelo Setup ele não consegue rodar modelos. Se algo der errado, execute `Scorch-Setup.exe --force` para baixar tudo de novo.

Opções do Setup: `--backend auto|cpu|vulkan|cuda`, `--data-dir <pasta>`, `--force`, `--no-pause`.

## Atalhos principais

| Tecla | Ação |
| --- | --- |
| `1`..`5` / `Alt+1`..`Alt+5` | Chat, Imagens, Modelos, Monitor, Logs |
| `F2` (no chat) | Mostra/oculta a lista de atalhos do chat |
| `Esc` (no chat) | Alterna o foco entre menus e a caixa de mensagem |
| `Enter` / `Esc` (em Imagens) | Inicia / termina a edição de um campo |
| `Ctrl+G` | Gera a imagem |
| `?` ou `F1` | Ajuda |
| `Ctrl+C` | Sair |

O título de cada conversa é criado automaticamente pelo modelo selecionado após a primeira resposta (renomeie com `Ctrl+R`).

## Compilar a partir do código

Requer Go 1.23+.

```sh
go mod tidy
sh scripts/build-dist.sh        # ou: powershell -ExecutionPolicy Bypass -File scripts\build-dist.ps1
```

Os dados (conversas, modelos, imagens, configuração) ficam em `studio-data`, ao lado do executável, ou na pasta indicada por `--data-dir` / `STUDIO_HOME`.
