# Installation Methods Comparison

openclio offers multiple installation methods depending on your needs.

## Method 1: Quick Install (Binary Only) — Current

**Best for:** Users who want minimal setup and smallest download.

```bash
curl -fsSL https://raw.githubusercontent.com/openclio/openclio/main/install.sh | sh
```

**What you get:**
- ~24MB single binary
- `openclio init` creates ~/.openclio/ with embedded templates
- Full personality via embedded SOUL.md

**Pros:**
- Fastest download
- Single file
- Works offline after init

**Cons:**
- Documentation only accessible via GitHub
- No local reference files

---

## Method 2: Full Package (Binary + Documentation) — PROPOSED

**Best for:** Users who want local documentation and examples.

```bash
# Download full package
curl -fsSL -o openclio-full.tar.gz \
  https://github.com/openclio/openclio/releases/latest/download/openclio-linux-amd64-full.tar.gz

# Extract and install
tar -xzf openclio-full.tar.gz
cd openclio-*-full
./install.sh
```

**What you get:**
- Binary
- docs/ folder with SOUL.md, VISION.md, AGENTS.md
- Example configs
- Local reference

**Pros:**
- All documentation offline
- Can read/modify philosophy files
- Better for air-gapped environments

**Cons:**
- Larger download (~30-50KB extra)
- More files to manage

---

## Method 3: Build from Source (Git Clone)

**Best for:** Developers, contributors, and users who want the latest.

```bash
git clone https://github.com/openclio/openclio.git
cd openclio
make build
make install
```

**What you get:**
- Complete source code
- All documentation
- Ability to modify and rebuild
- Latest (possibly unstable) features

**Pros:**
- Full transparency
- Can customize code
- Access to all files

**Cons:**
- Requires Go toolchain
- Larger download (~MBs)
- May be unstable if on main branch

---

## Recommendation

| User Type | Recommended Method |
|-----------|-------------------|
| First-time user | Method 1 (Quick) |
| Power user | Method 2 (Full) |
| Developer | Method 3 (Source) |
| Air-gapped | Method 2 (Full) |
| Contributor | Method 3 (Source) |

---

## What Gets Installed Where

### Binary Installation
```
/usr/local/bin/openclio          # Binary
~/.openclio/                     # Data directory
├── config.yaml                  # Configuration
├── identity.md                  # Agent personality (SOUL.md content)
├── user.md                      # User profile
├── memory.md                    # Long-term memory
├── PHILOSOPHY.md               # VISION.md content
├── AGENTS_REFERENCE.md         # AGENTS.md content
└── skills/                      # Skill templates
```

### Full Package Installation
```
/usr/local/bin/openclio          # Binary
~/.openclio/                     # Data directory (same as above)
~/.openclio/docs/                # Documentation copies
    ├── SOUL.md
    ├── VISION.md
    ├── AGENTS.md
    └── ...
```

---

## Verifying Your Installation

```bash
# Check binary
openclio version

# Check for complete install
openclio status

# Should show:
# - Binary location
# - Data directory
# - Config file
# - Identity file
# - Memory system status
# - Skills count
```

---

## Troubleshooting

### Missing identity.md or memory.md
If you installed before the template system:
```bash
# Re-run init to get templates
openclio init
```

### Want the full documentation?
```bash
# Option 1: Download from releases
curl -O https://github.com/openclio/openclio/releases/latest/download/openclio-linux-amd64-full.tar.gz

# Option 2: Clone repo
git clone https://github.com/openclio/openclio.git
cp openclio/{SOUL,VISION,AGENTS}.md ~/.openclio/
```
