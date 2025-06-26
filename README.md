# Validator Uptime Service

A service that automates the submission of validator uptime proofs and manages delegation rewards on the Avalanche network and Beam subnet. This service helps maintain validator rewards by regularly submitting signed uptime proofs and resolving delegation rewards.

## 🧭 Overview

1. **Fetch Validator Uptimes**: Retrieves validator uptime data from multiple Avalanche nodes.
2. **Generate Uptime Proofs**: Signs the best provable uptime from aggregated data.
3. **Submit Uptime Proofs**: Sends uptime proofs to the Beam network's staking manager contract.
4. **Resolve Delegation Rewards**: Resolves rewards for delegators only after valid uptime submission.

The service runs a continuous loop with a 24-hour cycle to automate these operations.

## ⚙️ Prerequisites

- Go 1.23.7 or higher
- Avalanche nodes (multiple preferred for aggregation)
- Access to a signature aggregator service
- Beam network credentials (private key)
- GraphQL endpoint for delegation data

## 📦 Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/BuildOnBeam/validator-uptime-service.git
   cd validator-uptime-service
   ```

2. Build the service:
   ```bash
   go build -o uptime-service
   ```

## 🔧 Configuration

Create a `config.json` file in the same directory as the binary with the following structure:

```json
{
  "avalanche_api_list": [
    "https://api.avax.network",
    "https://node1.avax.network",
    "https://node2.avax.network"
  ],
  "aggregator_url": "https://aggregator.example.com",
  "graphql_endpoint": "https://graph.onbeam.com/subgraphs/name/pos-testnet/graphql",
  "signing_subnet_id": "2JVSBoinj9C2J33VntvzYtVJNZdN2NKiwwKjcumHUWEb5DbBrm",
  "source_chain_id": "yH8D7ThNJkxmtkuv2jgBa4P1Rn3Qpr4pPr7QYNfcdoS6k6HWp",
  "quorum_percentage": 67,
  "beam_rpc": "https://eu.build.onbeam.com/rpc/testnet/your-api-key",
  "contract_address": "0x1234567890123456789012345678901234567890",
  "warp_messenger_address": "0x0987654321098765432109876543210987654321",
  "private_key": "your-private-key",
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

## Usage

Run the service with:

```bash
./uptime-service
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

### Communication Flow
```
┌────────────┐     ┌───────────────┐     ┌──────────────┐
│ Avalanche  │     │  Aggregator   │     │    Beam      │
│  P-Chain   │◄────┤    Service    │◄────┤   Network    │
└────────────┘     └───────────────┘     └──────────────┘
       ▲                   ▲                    ▲
       │                   │                    │
       └───────────────┬───────────────────────┘
                       │
                   ┌───┴───┐
                   │ Uptime │
                   │Service │
                   └───────┘
```

## 📁 Modules

- **`aggregator/`**: Handles uptime message creation and signature aggregation
- **`contract/`**: Submits proofs to Beam contracts via Warp protocol
- **`delegation/`**: Fetches delegator data and calls `resolveRewards`
- **`db/`**: Stores and loads signed uptime messages
- **`validator/`**: Queries uptime data from multiple Avalanche nodes
- **`main.go`**: Command runner with `generate`, `submit-uptime-proofs`, and `resolve-rewards` support
```
