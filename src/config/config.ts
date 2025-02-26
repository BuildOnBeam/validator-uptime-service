import { promises as fs } from 'fs';

export interface Config {
  avalancheApi: string;
  aggregatorUrl: string;
  signingSubnetId: string;
  quorumPercentage: number;
  beamRpc: string;
  stakingManagerAddress: string;
  privateKey: string;
  logLevel: string;
  networkId?: number;
  sourceChainId?: string;
}

export async function loadConfig(path: string): Promise<Config> {
  try {
    const fileContent = await fs.readFile(path, 'utf-8');
    const parsedConfig = JSON.parse(fileContent);
    
    const config: Config = {
      avalancheApi: parsedConfig.avalanche_api,
      aggregatorUrl: parsedConfig.aggregator_url,
      signingSubnetId: parsedConfig.signing_subnet_id,
      quorumPercentage: parsedConfig.quorum_percentage || 67,
      beamRpc: parsedConfig.beam_rpc,
      stakingManagerAddress: parsedConfig.contract_address,
      privateKey: parsedConfig.private_key,
      logLevel: parsedConfig.log_level || "info",
      networkId: parsedConfig.network_id || 5, // Default to Fuji testnet
      sourceChainId: parsedConfig.source_chain_id
    };
    
    console.log(`Loaded configuration from ${path}`);
    return config;
  } catch (error) {
    if (error instanceof Error) {
      throw new Error(`Failed to load config: ${error.message}`);
    }
    throw new Error('Unknown error loading config');
  }
}