import { ethers } from 'ethers';
import * as logging from '../logging/logging';

export class ContractClient {
  stakingManagerAddress: string;
  ethClient: ethers.JsonRpcProvider;
  privateKey: string;
  publicAddress: string;
  contract: ethers.Contract;
  signer: ethers.Wallet;
  
  constructor(rpcUrl: string, contractAddr: string, privateKeyHex: string) {
    // Remove 0x prefix if present
    if (privateKeyHex.startsWith("0x")) {
      privateKeyHex = privateKeyHex.substring(2);
    }
    privateKeyHex = `0x${privateKeyHex}`;
    
    this.stakingManagerAddress = contractAddr;
    this.privateKey = privateKeyHex;
    this.ethClient = new ethers.JsonRpcProvider(rpcUrl);
    this.signer = new ethers.Wallet(privateKeyHex, this.ethClient);
    this.publicAddress = this.signer.address;
    
    const contractABI = [
      {
        "inputs": [{"internalType": "bytes", "name": "signedMessage", "type": "bytes"}],
        "name": "submitUptimeProof",
        "outputs": [],
        "stateMutability": "nonpayable",
        "type": "function"
      }
    ];
    
    this.contract = new ethers.Contract(
      contractAddr,
      contractABI,
      this.signer
    );
  }
  
  async submitUptimeProof(signedMessage: Buffer): Promise<void> {
    try {
      const tx = await this.contract.submitUptimeProof(signedMessage);
      logging.infof("Submitted uptime proof transaction: %s", tx.hash);
      
      await tx.wait();
    } catch (error) {
      if (error instanceof Error) {
        throw new Error(`Contract call failed: ${error.message}`);
      }
      throw error;
    }
  }
}