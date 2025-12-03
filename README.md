# Validator Uptime Service

A Go service that automates the generation and submission of validator uptime proofs on the Avalanche network and the Beam L1. It aggregates uptime data, generates signed Warp messages, submits them on-chain, and resolves delegation rewards based on valid uptime proofs.

## 🧭 Overview

1. **Fetch Validator Uptimes**: Retrieves validator uptime data from multiple Avalanche nodes.
2. **Generate Uptime Proofs**: Signs the best provable uptime from aggregated data.
3. **Submit Uptime Proofs**: Sends uptime proofs to the Beam network's staking manager contract.
4. **Resolve Delegation Rewards**: Resolves rewards for delegators only after valid uptime submission.
5. **Submit Missing/Expired Proofs**: Detects gaps in subgraph uptime updates and re-submits proofs if needed.

The service runs a continuous loop with a 24-hour cycle to automate these operations.

## ⚙️ Prerequisites

- Go 1.24.9 or higher
- Multiple Avalanche nodes for uptime aggregation
- Access to a signature aggregator service
- Beam network credentials (private key)
- GraphQL endpoint for delegation data
- PostgreSQL database for storing signed uptime proofs

## 📦 Installation

Clone the repository:
  ```bash
  git clone https://github.com/BuildOnBeam/validator-uptime-service.git
  cd validator-uptime-service
  ```

## 🔧 Configuration

Create a `config.json` file in the root directory with the following structure:

```json
{
  "avalanche_api_list": [
    "https://api.avax.network",
    "https://node1.avax.network",
    "https://node2.avax.network"
  ],
  "aggregator_url": "http://localhost:9090/aggregate-signatures",
  "graphql_endpoint": "https://graph.onbeam.com/subgraphs/name/pos/graphql",
  "signing_subnet_id": "eYwmVU67LmSfZb1RwqCMhBYkFyG8ftxn6jAwqzFmxC9STBWLC",
  "source_chain_id": "2tmrrBo1Lgt1mzzvPSFt73kkQKFas5d1AP88tv9cicwoFp8BSn",
  "quorum_percentage": 67,
  "beam_rpc": "https://eu.build.onbeam.com/rpc/your-api-key",
  "contract_address": "0x2FD428A5484d113294b44E69Cb9f269abC1d5B54",
  "warp_messenger_address": "0x0200000000000000000000000000000000000005",
  "private_key": "0x-your-private-key",
  "log_level": "info",
  "network_id": 1,
  "database_url": "postgres://username:password@db-hostname:5432/db-name",
  "bootstrap_validators": [
    "bootstrap_validator_validationIds"
  ]
}
```

### 🧾 Config Parameters

| Parameter | Description |
|-----------|-------------|
| `avalanche_api_list` | List of Avalanche validator API endpoints for uptime queries |
| `aggregator_url` | Signature aggregator service URL |
| `graphql_endpoint` | GraphQL endpoint for fetching delegation data |
| `signing_subnet_id` | Subnet ID used for signing uptime messages |
| `source_chain_id` | Chain ID from which Warp messages originate |
| `quorum_percentage` | Required quorum threshold for aggregation |
| `beam_rpc` | Beam RPC endpoint for transaction submission |
| `contract_address` | Staking manager contract address |
| `warp_messenger_address` | Warp messenger contract address |
| `private_key` | Hex-encoded private key for signing transactions |
| `log_level` | Log verbosity level (e.g., `info`, `error`) |
| `network_id` | Network ID (1 for Mainnet, 5 for Fuji Testnet) |
| `database_url` | PostgreSQL connection string |
| `bootstrap_validators` | Validators excluded from uptime generation |

## 🚀 Usage

Run the service with:

```bash
go run main.go <command>
```

### Available commands:

| Parameter | Description |
|-----------|-------------|
| `generate-and-submit` | Full pipeline: fetch → sign → submit → store |
| `resolve-rewards` | Resolve delegator rewards for all validators |
| `submit-missing-uptime-proofs` | Re-submit missing or expired proofs |

Example:

```bash
go run main.go generate-and-submit
```

## 🧱 Technical Architecture

The service implements a modular architecture with the following components:

### AggregatorClient
- Establishes connection to the Subnet's signature aggregation service
- Implements `PackValidationUptimeMessage()` which generates a 46-byte uptime proof message using the Warp protocol
- Uses `SubmitAggregateRequest()` to send unsigned messages and retrieve signatures that meet quorum requirements

### ContractClient
- Handles the EVM contract communication via raw transaction assembly
- Implements `SubmitUptimeProof()` which formats and transmits uptime proofs to the staking manager contract
- Uses `TxToMethodWithWarpMessage()` to construct transactions containing Warp protocol messages

### DelegationClient
- Implements a GraphQL query interface via `GetDelegationsForValidator()`
- Contains batch processing logic for large delegation sets in `ResolveRewards()`
- Manages transaction nonce handling and gas optimization
- Implements error handling with backoff for failed rewards resolution

### Core Workflow
```
┌──────────────────┐
│ Avalanche Nodes  │
└───────▲──────────┘
        │ uptime samples
        │
┌───────┴───────────┐      ┌──────────────────────────────┐
│    validator/     │      │         aggregator/          │
│ Uptime Fetching   │─────▶│  Signs Warp uptime messages  │
└───────▲───────────┘      └───────────▲──────────────────┘
        │                              │ signed proof
        │                              │
┌───────┴───────────┐      ┌───────────┴───────────────────┐
│     service/      │─────▶│  contract/ (Beam RPC client)  │
│   Orchestrator    │      │     Submits uptime proofs     │
└───────▲───────────┘      └───────────▲───────────────────┘
        │                              │
        │ rewards resolution           │
┌───────┴───────────┐      ┌───────────┴───────────────────┐
│   delegation/     │────▶ │       db/ (PostgreSQL)        │
│ Fetch delegations │      │     Stores proof history      │
└───────────────────┘      └───────────────────────────────┘

```

#### Notable Behaviors

- Attempts descending sample order, upswing to 5% higher, and DB fallback when signing uptime proofs.
- Detects expired Warp messages and automatically re-signs.
- Tracks bootstrap validators to exclude them from uptime generation.
- Maintains persistent proof history to avoid duplicate submissions.

## 📁 Modules

- **`aggregator/`**: Handles uptime message creation and signature aggregation
- **`contract/`**: Submits proofs to Beam contracts via Warp protocol
- **`delegation/`**: Fetches delegator data and calls `resolveRewards`
- **`db/`**: Stores and loads signed uptime messages
- **`validator/`**: Queries uptime data from multiple Avalanche nodes
- **`main.go`**: Command runner with `generate-and-submit`, and `resolve-rewards` support
