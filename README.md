# Playerbots AzerothCore Windows Distribution

A complete, turnkey Windows distribution for running **[AzerothCore WotLK (3.3.5a)](https://github.com/azerothcore/azerothcore-wotlk)** pre-integrated with **[mod-playerbots](https://github.com/mod-playerbots/mod-playerbots)**. This project bundles a dedicated process supervisor launcher, automated MySQL initialization with dynamic performance tuning, and an easy-to-use Inno Setup Windows installer.

---

## 🌟 Features

- **Turnkey Server Environment**: Bundles AzerothCore WotLK, `mod-playerbots`, portable MySQL 8.0, runtime libraries, and configurations into a ready-to-run package.
- **Automated Startup Supervisor (`startup.exe`)**:
  - **Zero-Configuration Database Initialization**: Automatically initializes MySQL data directories, starts the daemon, creates default databases (`acore_world`, `acore_characters`, `acore_auth`, `acore_playerbots`), and sets up user privileges.
  - **Dynamic MySQL Tuning**: Detects host system RAM and automatically configures InnoDB buffer pool sizes and instances tailored for `mod-playerbots` workloads.
  - **Orchestrated Startup**: Starts MySQL first, waits for readiness, launches `authserver`, verifies port connectivity, and then launches `worldserver` with console access.
  - **Process Supervision & Auto-Restart**: Monitors server processes and automatically restarts `authserver` or `worldserver` if an unexpected crash or exit occurs.
  - **Clean, Ordered Shutdown**: Captures termination signals (e.g., `Ctrl+C`), gracefully stopping `authserver` and `worldserver` before performing a safe MySQL shutdown to protect against database corruption.
  - **Configuration Management**: Converts default `.conf.dist` files to active `.conf` files on first run and fixes relative directories (`data`, `logs`, `src`, `mysql`).
- **Windows Installer (`installer.iss`)**:
  - Easy graphical installation wizard for 64-bit Windows.
  - Optional automated download and extraction of required client data files (maps, vmaps, mmaps, and DBC).
  - Creates desktop and Start Menu shortcuts.

---

## 📁 Repository Structure

```
playerbots/
├── azerothcore-wotlk/    # AzerothCore core repository (Git submodule, branch: Playerbot)
├── mod-playerbots/       # Playerbots AI module (Git submodule, branch: master)
├── cmd/
│   └── startup/          # Go source code for the startup supervisor & orchestrator
├── deps/                 # Local build dependencies (MySQL, OpenSSL, client data)
├── dist/                 # Staged distribution files (binaries, configs, MySQL, DLLs)
├── output/               # Generated setup installer executables
├── CMakeLists.txt        # Superbuild CMake script orchestrating build and packaging
├── installer.iss         # Inno Setup installer script
├── startup.exe           # Compiled Windows launcher binary
└── README.md             # Project documentation
```

---

## 🚀 Quick Start (For Players & Server Admins)

### Option 1: Using the Installer

1. Download or build the installer (`playerbots-setup-0.1.0.exe`).
2. Run the installer and select your installation directory (default: `C:\Program Files\Playerbots`).
3. (Recommended) Check **"Download client data (maps, vmaps, mmaps, dbc)"** to allow the installer to automatically fetch and unpack map extraction files.
4. Finish installation and launch **Playerbots Server Launcher** from the Start Menu or Desktop.

### Option 2: Portable / Manual Run

1. Ensure your extracted client data files (`dbc`, `maps`, `vmaps`, `mmaps`) are placed inside the `data/` folder.
2. Run `startup.exe` from the root of the distribution directory.
3. The supervisor will:
   - Initialize the MySQL database (if running for the first time).
   - Generate default `.conf` configuration files.
   - Start MySQL, Authserver, and Worldserver.
4. When the server finishes booting, you will see the interactive Worldserver console (`AC>`).

---

## 🎮 Connecting to the Server

1. **Configure Realmlist**:
   Open your World of Warcraft 3.3.5a client directory, edit `Data/enUS/realmlist.wtf` (or your locale's equivalent) and set:
   ```text
   set realmlist 127.0.0.1
   ```

2. **Create an Account**:
   In the `startup.exe` console window (Worldserver prompt), type:
   ```text
   account create <username> <password>
   account set gmlevel <username> 3 -1
   ```

3. **Log In & Play**:
   Start your WoW 3.3.5a client (`Wow.exe`), enter your credentials, and start playing!

---

## 🤖 Using Playerbots

`mod-playerbots` populates the world with autonomous player bots that level, quest, run dungeons, participate in PvP, and can be invited into your party/raid.

### Useful In-Game Commands

| Command | Description |
| :--- | :--- |
| `.bot add <name>` | Spawn an existing offline character as a bot |
| `.bot remove <name>` | Remove a bot from the world |
| `.bot init` | Initialize playerbot accounts / random bots |
| `.playerbot bot add <class>` | Spawn a random bot of a specific class |
| `.playerbot bot remove` | Remove targeted bot |
| `.bot party <action>` | Manage bot party behavior (e.g., follow, attack, stay) |

For comprehensive documentation, strategies, and bot commands, visit the [mod-playerbots documentation](https://github.com/mod-playerbots/mod-playerbots).

---

## ⚙️ Configuration & Startup Options

### Server Configurations
Configuration files are located in the `configs/` directory:
- `configs/worldserver.conf`: Core worldserver settings, rates, networking, and gameplay rules.
- `configs/authserver.conf`: Authentication server settings and database connection strings.
- `configs/modules/playerbots.conf`: Detailed Playerbots AI parameters, bot limits, leveling rates, and behaviors.
- `mysql/my.cnf`: MySQL / InnoDB fine-tuning parameters.

### Launcher CLI Arguments (`startup.exe`)

`startup.exe` accepts command-line flags for custom environments:

```text
Usage of startup.exe:
  -mysql-dir string
        Path to MySQL root directory. Default: ./mysql or $env:MYSQL_ROOT
  -data-dir string
        Path to MySQL data directory. Default: <mysql-dir>/data
  -mysql-cnf string
        Path to MySQL configuration file (my.cnf or my.ini). Default: auto-detected
  -port int
        MySQL server port. (default 3306)
  -auth-port int
        Authserver realm port. (default 3724)
  -timeout int
        Timeout in seconds to wait for MySQL readiness. (default 30)
  -init-only
        Initialize MySQL data dir, apply database SQL script, and exit immediately.
  -skip-sql
        Skip executing the embedded create_mysql.sql script.
```

---

## 🛠️ Building from Source

### Prerequisites

- **OS**: Windows 10 / 11 (64-bit)
- **C++ Compiler**: Visual Studio 2022 (MSVC v143 toolset) with C++ Desktop Development workload
- **CMake**: Version 3.16 or higher
- **Go**: Version 1.20 or higher
- **Git**: With submodule support enabled
- **Inno Setup**: Version 6.x (for building the installer)
- **Dependencies**:
  - Boost (1.84.0+)
  - OpenSSL (3.x x64)
  - MySQL Server (8.0.x x64)

### Build Steps

1. **Clone the repository with submodules**:
   ```bash
   git clone --recurse-submodules https://github.com/your-username/playerbots.git
   cd playerbots
   ```

2. **Configure with CMake**:
   ```powershell
   cmake -B build -S . `
     -G "Visual Studio 17 2022" -A x64 `
     -DCMAKE_BUILD_TYPE=RelWithDebInfo `
     -DMYSQL_ROOT_DIR="deps/mysql-8.0.46-winx64" `
     -DOPENSSL_ROOT_DIR="deps/openssl-3.5.7/x64" `
     -DBOOST_ROOT_DIR="C:/local/boost_1_84_0"
   ```

3. **Build and Install to `dist/`**:
   ```powershell
   cmake --build build --config RelWithDebInfo --target install
   ```

4. **Build the Inno Setup Installer**:
   ```powershell
   & "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" installer.iss
   ```
   The compiled installer will be generated in `output/playerbots-setup-0.1.0.exe`.

---

## 📜 License

This project is licensed under the [MIT License](LICENSE).

### Upstream Licenses & Acknowledgments
- **[AzerothCore](https://www.azerothcore.org/)**: GNU AGPL v3.0
- **[mod-playerbots](https://github.com/mod-playerbots/mod-playerbots)**: GNU AGPL v3.0 / GPL
- **[World of Warcraft Client Data](https://github.com/wowgaming/client-data)**: Provided by the wowgaming community
