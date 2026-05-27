# MultiPacMan 🎮

Jeu multijoueur asymétrique inspiré de Pac-Man avec mécanique **Undercover**.

- **Serveur** : Go (WebSocket autoritaire, 30 Hz)
- **Client** : Python + Pyxel (rendu pixel-art)

---

## Structure du projet

```
MultiPacMan/
├── server/          # Backend Go
│   ├── main.go      # Serveur HTTP/WS + Hub
│   ├── types.go     # Types & constantes partagés
│   ├── client.go    # Connexion WebSocket par joueur
│   ├── game.go      # Logique autoritaire + tick loop 30 Hz
│   ├── map.go       # Génération BSP + culling
│   ├── roles.go     # Distribution des rôles
│   └── abilities.go # Capacités des fantômes
│
└── client/          # Frontend Python/Pyxel
    ├── main.py          # Boucle Pyxel + App
    ├── state.py         # GameState partagé (thread-safe)
    ├── network.py       # Thread réseau WebSocket
    ├── input_handler.py # Capture inputs + séquençage
    └── renderer.py      # Rendu pixel-art (primitives géométriques)
```

---

## Démarrage rapide

### 1. Serveur Go

```powershell
cd server
go run .
```

Le serveur écoute sur \*\*\*\* :

- `wss://pacman.yulian-server.duckdns.org/ws?room=<roomID>` — WebSocket joueur
- `http://pacman.yulian-server.duckdns.org/health` — healthcheck
- `http://pacman.yulian-server.duckdns.org/rooms` — liste des rooms actives

### 2. Client Python (autant de fois que nécessaire)

```powershell
cd client
uv run python main.py
# Options :
uv run python main.py --server wss://pacman.yulian-server.duckdns.org/ws --room maroom
```

---

## Règles du jeu

### Rôles (distribués aléatoirement)

| Rôle          | Quantité      | Objectif                          |
| ------------- | ------------- | --------------------------------- |
| **Pacman**    | `max(1, N/4)` | Collecter toutes les gommes       |
| **Traqueur**  | Rotatif       | Traquer via empreintes thermiques |
| **Bâtisseur** | Rotatif       | Placer des murs destructibles     |
| **Sprinteur** | Rotatif       | Attraper via dash                 |

### Asymétrie (Undercover)

- Tous les joueurs partagent le même sprite — impossible de distinguer Pacman d'un fantôme à vue
- Les gommes ne sont **visibles que pour les Pacmans** (filtrées côté serveur)
- Si un fantôme attaque un autre fantôme → **étourdissement mutuel** de 3 secondes

### Conditions de victoire

- **Pacmans** gagnent en collectant toutes les gommes
- **Fantômes** gagnent en étourdissant simultanément tous les Pacmans

---

## Contrôles (in-game)

| Touche                     | Action                                  |
| -------------------------- | --------------------------------------- |
| `WASD` / Flèches           | Déplacement                             |
| `ESPACE`                   | Activer la capacité (Dash / Scan / Mur) |
| `ENTER` / `ESPACE` (lobby) | Prêt                                    |
| `S` (lobby)                | Forcer le démarrage                     |
| `Q` / `ÉCHAP`              | Quitter                                 |

---

## Taille de la carte (dynamique)

| Joueurs | Carte     |
| ------- | --------- |
| 4       | 60 × 60   |
| 8       | 88 × 88   |
| 12      | 108 × 108 |

---

## Architecture réseau

```
Client (Pyxel 30 Hz)
  │  update()  →  InputHandler  →  NetworkManager.send()
  │                                        │
  │                                   asyncio Queue
  │                                        │
  │                               WebSocket Thread (asyncio)
  │                                        │
  │                               Server Go (30 Hz tick)
  │                                        │
  └── draw()  ←  GameState.get_snapshot() ←┘
```

### Payload JSON (Client → Serveur)

```json
{ "type": "INPUT", "seq": 42, "dir_x": 1.0, "dir_y": 0.0, "dash": false }
```

### Payload JSON (Serveur → Client)

```json
{
  "type": "GAME_STATE",
  "tick": 1200,
  "players": [{ "id": "abc", "x": 10.5, "y": 8.2 }],
  "tiles": [{ "x": 10, "y": 8, "t": 2 }],
  "footprints": [],
  "status": {
    "score": 120,
    "stunned": false,
    "cooldown_ms": 0,
    "role": "PACMAN"
  },
  "last_seq": 42
}
```

---

## Roadmap

- [x] **Phase 1** — WebSocket multi-room, mouvement validé serveur
- [x] **Phase 2** — Génération BSP, culling (brouillard de guerre)
- [x] **Phase 3** — Rôles Undercover, asymétrie des gommes
- [x] **Phase 4** — Capacités (Traqueur / Bâtisseur / Sprinteur)
- [x] **Phase 5** — Prédiction côté client + réconciliation serveur
- [ ] Lobby persistant / reconnexion
- [ ] Sprites pixel-art `.pyxres`
- [ ] Protobuf (remplacement JSON pour réduire la bande passante)
- [ ] Son & musique
