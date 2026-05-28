# MultiPacMan

Jeu multijoueur asymétrique compétitif inspiré de Pac-Man avec mécanique **Undercover**. Les joueurs sont divisés en deux équipes : les **Pacmans** (objectif : manger les gommes) et les **Fantômes** (objectif : capturer les Pacmans), mais **personne ne sait qui est qui** — tous partagent le même sprite !

- **Serveur** : Go (WebSocket autoritaire, tick loop 30 Hz)
- **Client** : Python + Pyxel (rendu pixel-art 60 fps)
- **Réseau** : WebSocket TCP + UDP optionnel pour inputs rapides

---

## Table des matières

1. [Installation](#installation)
2. [Démarrage rapide](#démarrage-rapide)
3. [Architecture](#architecture)
4. [Rôles & Capacités](#rôles--capacités)
5. [Mécanique de jeu](#mécanique-de-jeu)
6. [Contrôles](#contrôles)
7. [Paramètres de configuration](#paramètres-de-configuration)
8. [Compilation EXE](#compilation-exe)

---

## Installation

### Prérequis

**Serveur :**

- Go 1.20+

**Client :**

- Python 3.11+
- `uv` (gestionnaire de dépendances Python)

### Cloner & installer

```powershell
# Cloner le repo
git clone <repo-url>
cd MultiPacMan

# Serveur : pas de dépendances externes (Go std lib + Gorilla websockets)
cd server
go mod download

# Client : installer via uv
cd ../client
uv sync
```

---

## Démarrage rapide

### Lancer le serveur

```powershell
cd server
go run . --env dev
```

**Options serveur :**

- `--env dev` : localhost:8080 (développement local)
- `--env lan` : détecte IP locale (réseau local)
- `--env prod` : (défaut) mode production distant

**Exemple :**

```powershell
# Développement (localhost)
go run . --env dev

# Réseau local
go run . --env lan
# Affiche : ws://192.168.x.x:8080/ws?room=<roomID>

# Production
go run .
# Affiche : wss://pacman.yulian-server.duckdns.org/ws?room=<roomID>
```

### Lancer le(s) client(s)

**Depuis Python (développement) :**

```powershell
cd client

# Serveur local (dev) - mode dev
uv run python main.py --server localhost:8080 --room mygame

# Serveur réseau local - mode lan
uv run python main.py --server 192.168.10.20:8080 --room mygame

# Serveur distant (défaut) - mode prod
uv run python main.py --room mygame
```

**Depuis EXE compilé (production) :**

```powershell
# Localhost
.\dist\client\client.exe --host localhost --room mygame

# Réseau local
.\dist\client\client.exe --host 192.168.10.20 --room mygame

# Serveur distant (défaut)
.\dist\client\client.exe --room mygame
```

---

## Architecture

### Structure du projet

```
MultiPacMan/
├── README.md
│
├── server/                   # Backend Go
│   ├── main.go               # Serveur HTTP/WS + Hub gestion rooms
│   ├── types.go              # Types partagés, constantes
│   ├── client.go             # Gestion WebSocket par joueur
│   ├── game.go               # Logique autoritaire, tick loop 30Hz
│   ├── map.go                # Génération BSP, culling visibilité
│   ├── roles.go              # Distribution aléatoire des rôles
│   ├── abilities.go          # Implémentations des capacités
│   └── go.mod
│
└── client/                   # Frontend Python/Pyxel
    ├── main.py               # Boucle Pyxel 60Hz + App
    ├── state.py              # GameState thread-safe
    ├── network.py            # WebSocket asyncio + UDP
    ├── input_handler.py      # Capture inputs + séquençage 30Hz
    ├── renderer.py           # Rendu pixel-art Pyxel
    ├── my_resource.pyxres    # Ressources Pyxel (musique, etc.)
    ├── pyproject.toml        # Dépendances
    └── dist/                 # (après compilation)
        └── client/
            └── client.exe
```

### Flux de données

```
┌─────────────────────────────────────────────────────────────────┐
│                        Client (Python/Pyxel)                    │
│                                                                 │
│  InputHandler ──┐                              ┌─ Renderer      │
│  (WASD/Arrows)  │          GameState           │  (Render 60Hz) │
│                 ├──────────────────────────────┤                │
│              Pyxel                             Pyxel            │
│             (60 fps)                         (60 fps)           │
│                 │                              │                │
│                 └──────── NetworkManager ──────┘                │
│                       (Send 30Hz via WS/UDP)                    │
│                         (Recv via asyncio)                      │
└─────────────────────────────────┬───────────────────────────────┘
                                  │ WebSocket (TCP)
                                  │ + UDP (optional)
                                  ▼
┌──────────────────────────────────────────────────────────────────┐
│                    Server (Go - game.go)                         │
│                                                                  │
│  Game Loop (30 Hz tick)                                          │
│  ├─ Move players (collision detection)                           │
│  ├─ Check item collection                                        │
│  ├─ Check combat (stun mechanics)                                │
│  ├─ Apply abilities (dash, phasing, traps)                       │
│  ├─ Culling: build GameStatePayload per player                   │
│  │  (visible entities, tiles, items within vision radius)        │
│  └─ Broadcast to all players                                     │
└──────────────────────────────────────────────────────────────────┘
```

### Cycle de jeu (30 Hz serveur)

1. **Réception** : inputs depuis tous les clients
2. **Mouvement** : appliquer directions, dash, phasing
3. **Collisions** : vérifier murs, combats (fantôme vs fantôme)
4. **Gommes** : collecter si Pacman, vérifier victoire
5. **Items** : respawn cerises/coffres
6. **Culling** : pour chaque joueur, calculer entités/tuiles visibles
7. **Broadcast** : envoyer GameStatePayload à tous

---

## Rôles & Capacités

### Équipe PACMAN (Objectif : Manger toutes les gommes)

#### **PACMAN** (rôle de base)

| Attribut            | Valeur                                                 |
| ------------------- | ------------------------------------------------------ |
| **Vitesse**         | 0.110 tiles/tick (~3.3 tiles/sec)                      |
| **Rayon de vision** | 15 tiles                                               |
| **Capacité**        | Invisibilité (30s) — activée via `E`                   |
| **Charges invis**   | 7 charges max                                          |
| **Récupération**    | Gommes : +5 pts, Cerises : +50 pts, Coffres : +1 invis |
| **Points victoire** | 3000 points OU toutes gommes collectées                |

#### **BUILDER** (Pacman avec construction)

| Attribut              | Valeur                                     |
| --------------------- | ------------------------------------------ |
| **Vitesse**           | 0.078 tiles/tick (~2.3 tiles/sec)          |
| **Rayon de vision**   | 9 tiles                                    |
| **Capacité**          | Placer murs destructibles (click-to-build) |
| **Cooldown capacité** | 10 secondes                                |
| **Spécificité**       | Ne voit PAS les coffres (pénalité)         |
| **Contrôle**          | Click sur la carte pour placer un mur      |

### Équipe GHOST (Objectif : Capturer les Pacmans)

#### **GHOST_TRACKER** (Traqueur)

| Attribut              | Valeur                                                 |
| --------------------- | ------------------------------------------------------ |
| **Vitesse**           | 0.080 tiles/tick (~2.4 tiles/sec)                      |
| **Rayon de vision**   | 9 tiles                                                |
| **Capacité**          | **Indicateur directionnel** (angle) vers Pacman proche |
| **Cooldown capacité** | 60 secondes                                            |
| **Durée active**      | 30 secondes après activation (ESPACE)                  |
| **Révèle**            | Angle du Pacman le plus proche (envoyé via status)     |

#### **GHOST_SPRINTER** (Sprinteur)

| Attribut              | Valeur                                         |
| --------------------- | ---------------------------------------------- |
| **Vitesse**           | 0.096 tiles/tick (~2.9 tiles/sec)              |
| **Rayon de vision**   | 12 tiles                                       |
| **Capacité**          | **DASH** : accélération 3× pour 0.3s (9 ticks) |
| **Cooldown capacité** | 8 secondes                                     |
| **Activation**        | ESPACE (direction courante)                    |
| **Pénalité**          | Collision mur = s'étourdit soi-même 3 secondes |

#### **GHOST_TRAPPER** (Piégeur)

| Attribut              | Valeur                                      |
| --------------------- | ------------------------------------------- |
| **Vitesse**           | 0.080 tiles/tick (~2.4 tiles/sec)           |
| **Rayon de vision**   | 10 tiles                                    |
| **Capacité**          | Placer une **fausse gomme**                 |
| **Cooldown capacité** | 12 secondes                                 |
| **Spécificité**       | Gommes visibles uniquement si on est Pacman |

#### **GHOST_PHASER** (Phaseur)

| Attribut              | Valeur                                     |
| --------------------- | ------------------------------------------ |
| **Vitesse**           | 0.080 tiles/tick (~2.4 tiles/sec)          |
| **Rayon de vision**   | 10 tiles                                   |
| **Capacité**          | **PHASING** : traverser murs 20 secondes   |
| **Cooldown capacité** | 60 secondes                                |
| **Durée active**      | 20 secondes (600 ticks @ 30Hz)             |
| **Activation**        | ESPACE                                     |
| **Mécanique**         | Invisible aux autres si dans un mur normal |
| **Limitation**        | NE PEUT PAS traverser les murs du Builder  |

---

## Mécanique de jeu

### Distribution des rôles

Formule : `Pacmans = max(1, N/4)` où N = nombre de joueurs

| Joueurs | Pacmans | Fantômes | Composition                        |
| ------- | ------- | -------- | ---------------------------------- |
| 4       | 1       | 3        | 1 (Pacman/Builder 50%), 3 GHOST    |
| 5-8     | 2       | 3-6      | 2 (Pacman/Builder 50%), rest GHOST |
| 9-12    | 3       | 6-9      | 3 (Pacman/Builder 50%), rest GHOST |

Les rôles **Pacman et Builder** comptent comme équipe PACMAN.

**Builder** : Est un rôle PACMAN attribué aléatoirement (50% chance si pacman assigné).

- Il n'existe qu'une seule version du Builder (pas de variant Ghost)
- Vision réduite (9 tiles vs 15 pour Pacman normal)
- Capacité : placement de murs via click-to-build

### Conditions de victoire

#### **PACMAN gagne si :**

- Toutes les gommes sont collectées **OU**
- Un Pacman atteint **3000 points**

#### **GHOST gagne si :**

- Tous les Pacmans sont **morts** (tués par collision avec un fantôme)

#### Cerises (Cherries)

- **Respawn** : 30s après game start, puis toutes les 15-20s
- **Points** : 50 pts
- **Visibilité** : Pacman uniquement
- **Effet** : +1 charge d'invisibilité

#### Coffres (Chests)

- **Respawn** : toutes les 28s
- **Visibilité** : Pacman uniquement (**Builder ne les voit PAS**)
- **Effet** : +1 charge d'invisibilité

### Mécanique de combat

**Quand un joueur attaque un autre joueur :**

- Pacman vs Fantôme : Le Pacman **meurt**, le fantôme attaquant est **étourdit 20 secondes**
- Fantôme vs Fantôme : Le fantôme victime **meurt**, l'attaquant est **étourdit 20 secondes**
- Pendant stun : ne peut PAS se déplacer, ne peut PAS utiliser capacités
- Visual : clignotement rouge/blanc
- **Condition de victoire fantômes** : Tous les Pacmans sont **morts**

### Visibilité & Culling

Chaque joueur reçoit uniquement les données des entités/tuiles dans son rayon de vision :

#### Rayon de vision

- Filtre circulaire : `distance(player, entity) <= vision_radius`
- Buffer : 8 tiles supplémentaires pour éviter lag

#### Anti-cheat côté serveur

**Gommes :**

- Ghosts ne voient jamais les tuiles `TilePellet`
- Remplacées par `TileEmpty` dans la payload

**Fausses gommes :**

- Pacmans voient `TileFakePellet` comme `TilePellet`
- Ghosts voient `TileFakePellet` comme `TileEmpty`

**Cerises & Coffres :**

- Visibles **seulement pour Pacmans et Builder** (pas pour autres Ghosts)
- Builder n'a pas accès aux coffres en tant que Pacman

**Phasing :**

- Phaseur invisible si en train de traverser un mur
- Les autres joueurs ne voient pas la durée du phasing
- Autres joueurs ne savent pas qu'il phasing

### Mur destructible (Builder)

- **Placement** : via click-to-build (4 coords pour direction)
- **Durée de vie** : permanent (jusqu'à être détruit)
- **Traversable** : par aucun joueur (y compris Phaseur)
- **Destruction** : collision = destruction du mur
- **Type** : TileDestructibleWall
- **Impact stratégique** : ralentit le déplacement, bloque Phaseur

---

## Contrôles

### Lobby (avant game start)

| Touche             | Action             |
| ------------------ | ------------------ |
| `ESPACE` / `ENTER` | Marquer comme PRÊT |
| `ÉCHAP`            | Quitter            |

### In-Game

| Touche          | Action                           |
| --------------- | -------------------------------- |
| `W` / `Z`       | Haut                             |
| `A` / `Q`       | Gauche                           |
| `S`             | Bas                              |
| `D`             | Droite                           |
| `↑↓←→`          | Flèches alternativement          |
| `ESPACE`        | Activer capacité (Dash/Phasing)  |
| `E`             | Invisibilité (Pacman uniquement) |
| `CLIQUE SOURIS` | Placer un mur (Builder)          |
| `ÉCHAP`         | Quitter                          |

### Spécificités

**Builder (Pacman uniquement)** :

- Click-to-build : Click sur la carte pour placer un mur destructible
- Coords : [x1, y1, x2, y2] (4 positions pour définir la direction)
- Cooldown : 10 secondes entre placements

**Tracker (Ghost)** :

- ESPACE = active l'indicateur directionnel 30 secondes
- L'angle s'affiche via le champ `tracker_dir_angle` dans le status
- Pointe vers le Pacman le plus proche

---

## Paramètres de configuration

### Serveur

**Lancer avec options :**

```bash
go run . --env <dev|lan|prod>
```

**Constantes (hardcodées dans types.go) :**

```go
// Vision
const (
    VisionPacman   = 15.0   // Pacman voit plus loin
    VisionTracker  = 9.0    // Tracker vision réduite
    VisionBuilder  = 9.0
    VisionSprinter = 12.0
)

// Vitesse (tiles/tick @ 30 Hz)
const (
    SpeedPacman   = 0.110   // ~3.3 tiles/sec
    SpeedTracker  = 0.080   // ~2.4 tiles/sec
    SpeedBuilder  = 0.078   // ~2.3 tiles/sec
    SpeedSprinter = 0.096   // ~2.9 tiles/sec
    SpeedDash     = 0.350   // ~10.5 tiles/sec (dash uniquement)
)

// Items
const (
    CherryFirstSpawnTicks  = 30 * TicksPerSec  // 30s après start
    CherryIntervalMinTicks = 15 * TicksPerSec  // min 15s entre respawns
    CherryIntervalMaxTicks = 20 * TicksPerSec  // max 20s entre respawns
    CherryPoints           = 50

    ChestSpawnIntervalTicks = 28 * TicksPerSec // 28s entre coffres

    InvisMaxCharges  = 7                        // charges invis Pacman
    InvisDurationSec = 30                       // durée invis active
)

// Combat
const (
    StunSeconds = 3              // durée stun fantôme vs fantôme
    GhostKillStunSec = 20        // stun étendu pour kill
)

// Capacités
Tracker cooldown:  60_000 ms (1 min)
Builder cooldown:  10_000 ms (10s)
Sprinter cooldown: 8_000 ms (8s)
Trapper cooldown:  12_000 ms (12s)
Phaser cooldown:   60_000 ms (1 min)
```

### Client

**Lancer avec paramètres :**

```bash
# Development mode (localhost)
uv run python main.py --server ws://localhost:8080/ws --room myroom --env dev

# LAN mode (IP locale)
uv run python main.py --server ws://192.168.10.20:8080/ws --room myroom --env lan

# Production (serveur distant)
uv run python main.py --room myroom
# Utilise l'URL par défaut : wss://pacman.yulian-server.duckdns.org/ws
```

**Options Python :**

```
--server URL   : URL WebSocket (défaut: wss://pacman.yulian-server.duckdns.org/ws)
--room ID      : ID room (défaut: "default")
--env ENV      : dev/lan/prod (affecte URL par défaut)
```

**Paramètres EXE :**

```bash
# Défaut : prod distant
client.exe

# Localhost
client.exe --host localhost --port 8080 --room game1

# IP locale
client.exe --host 192.168.10.20 --port 8080 --room game1

# Serveur distant
client.exe --host pacman.yulian-server.duckdns.org --room game1
```

| Paramètre  | Défaut                           | Description                       |
| ---------- | -------------------------------- | --------------------------------- |
| `--host`   | pacman.yulian-server.duckdns.org | Hostname/IP serveur               |
| `--port`   | 443                              | Port WebSocket (443 = SSL/TLS)    |
| `--room`   | random UUID                      | Room ID                           |
| `--secure` | true (si port 443)               | Utiliser WSS (WebSocket Sécurisé) |

---

## Compilation EXE

### Prérequis

- Python 3.11+
- PyInstaller 6.20+
- Tous les dépendances Pyxel/WebSockets installées

### Build

```powershell
cd client

# Installer PyInstaller
uv pip install pyinstaller

# Builder l'EXE (one-file executable)
uv run pyinstaller --onefile --windowed \
    --name client \
    main.py

# L'exécutable sera dans : dist/client.exe
```

### Distribution

```powershell
# Copier à d'autres machines
cp dist/client/client.exe ../../autre_pc/

# Lancer
./client.exe --host 192.168.10.20 --port 8080 --room gameid
```

---

## Licence & Crédits

- Inspiré de Pac-Man (Namco)
- Mécanique Undercover adaptée du jeu Mafia/Werewolf
- Développé avec Go, Python, Pyxel

---
