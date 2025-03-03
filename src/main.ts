import * as logging from './logging/logging';
import * as config from './config/config';
import { AggregatorClient } from './aggregator/client';
import { ContractClient } from './contract/client';
import { fetchUptimes } from './validator/fetcher';
import { handleError } from './errutil/errutil';

async function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function main(): Promise<void> {
  try {
    const cfg = await config.loadConfig('config.json');
    
    logging.setLevel(cfg.logLevel);
    
    const aggClient = new AggregatorClient(
      cfg.aggregatorUrl,
      cfg.beamRpc,
      cfg.messagingContractAddress,
      cfg.signingSubnetId,
      cfg.quorumPercentage,
      // cfg.networkId,
      // cfg.sourceChainId
    );
    
    const contractClient = new ContractClient(
      cfg.beamRpc,
      cfg.stakingManagerAddress,
      cfg.privateKey
    );
    
    logging.infof("Connected to staking manager contract at %s", cfg.stakingManagerAddress);
    logging.infof("Connected to validator messages contract at %s", cfg.messagingContractAddress);
    
    logging.info("Starting uptime-service loop...");
    
    while (true) {
      try {
        // 1. Fetch current validators and their uptime from Avalanche P-Chain
        const validators = await fetchUptimes(cfg.avalancheApi);
        logging.infof("Fetched %d validators' uptime info", validators.length);
        
        // 2. For each validator, build message, aggregate signatures, and submit proof
        for (const val of validators) {
          try {
            // Build the unsigned uptime message for this validator by calling packValidationUptimeMessage on ValidatorMessages.sol
            const msgBytes = await aggClient.packValidationUptimeMessage(val.validationId, val.uptimeSeconds);
            logging.infof("Built uptime message for validator %s (uptime=%d seconds)", 
              val.nodeId, val.uptimeSeconds);
            
            // 3. Submit to signature-aggregator service to get aggregated signature
            const signedMsg = await aggClient.submitAggregateRequest(msgBytes);
            logging.infof("Received aggregated signature for validator %s", val.nodeId);
            logging.infof("signedmsg: %s", signedMsg.toString('hex'));
            
            // 4. Submit the signed uptime proof to the smart contract
            await contractClient.submitUptimeProof(signedMsg);
            logging.infof("Submitted uptime proof for validator %s to contract", val.nodeId);
          } catch (error) {
            handleError(`processing validator ${val.nodeId}`, error as Error);
            continue;
          }
        }
      } catch (error) {
        logging.errorf("Error fetching validator uptimes: %s", (error as Error).message);
      }
      
      logging.info("Uptime proof cycle completed. Sleeping for 24 hours...");
      await sleep(24 * 60 * 60 * 1000);
    }
  } catch (error) {
    console.error("Fatal error:", (error as Error).message);
    process.exit(1);
  }
}

main().catch(error => {
  console.error("Unhandled error in main:", error);
  process.exit(1);
});