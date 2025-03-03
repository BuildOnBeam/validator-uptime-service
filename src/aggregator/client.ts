import axios from 'axios';
import * as crypto from 'crypto';
import * as bs58 from 'bs58';
import { ethers } from 'ethers';

interface AggregatorRequest {
  message: string;
  justification?: string;
  "signing-subnet-id"?: string;
  "quorum-percentage"?: number;
}

interface AggregatorResponse {
  "signed-message": string;
}

const CODEC_VERSION = 0;
const VALIDATION_UPTIME_MESSAGE_TYPE_ID = 0;

const messagingContractABI = [
  {
    "inputs": [
      {
        "internalType": "bytes32",
        "name": "validationID",
        "type": "bytes32"
      },
      {
        "internalType": "uint64",
        "name": "uptime",
        "type": "uint64"
      }
    ],
    "name": "packValidationUptimeMessage",
    "outputs": [
      {
        "internalType": "bytes",
        "name": "",
        "type": "bytes"
      }
    ],
    "stateMutability": "pure",
    "type": "function"
  }
];

export class AggregatorClient {
  baseUrl: string;
  signingSubnetId: string;
  quorumPercentage: number;
  messagingContract: ethers.Contract;
  
  constructor(
    baseUrl: string,
    rpcUrl: string,
    messagingContractAddress: string,
    signingSubnetId: string = "",
    quorumPercentage: number = 0
  ) {
    this.baseUrl = baseUrl;
    this.signingSubnetId = signingSubnetId;
    this.quorumPercentage = quorumPercentage;
    
    // Initialize the ethers provider and contract
    const provider = new ethers.JsonRpcProvider(rpcUrl);
    this.messagingContract = new ethers.Contract(
      messagingContractAddress,
      messagingContractABI,
      provider
    );
  }
  
  /**
   * Pack a validation uptime message by calling the contract's utility function.
   * 
   * @param validationId The validation ID as a base58 string
   * @param uptimeSeconds The uptime in seconds
   * @returns A Buffer containing the packed message
   */
  async packValidationUptimeMessage(validationId: string, uptimeSeconds: number): Promise<Uint8Array> {
    // Process validationId into a bytes32 format
    let validationIdBytes: Uint8Array;
    
    // 1. Remove "NodeID-" prefix if present
    if (validationId.startsWith("NodeID-")) {
      validationId = validationId.substring("NodeID-".length);
    }
    
    // 2. Decode the base58 string
    const decoded = bs58.decode(validationId);
    if (decoded.length < 4) {
      throw new Error("Decoded validationID is too short");
    }
    
    // 3. Separate data bytes and the 4-byte checksum
    const data = decoded.slice(0, decoded.length - 4);
    const checksum = decoded.slice(decoded.length - 4);
    
    // 4. Verify checksum (last 4 bytes of SHA-256 of data)
    const hash = crypto.createHash('sha256').update(Buffer.from(data)).digest();
    const expectedChecksum = hash.slice(hash.length - 4);
    
    if (!Buffer.from(checksum).equals(Buffer.from(expectedChecksum))) {
      throw new Error("ValidationID checksum mismatch");
    }
    
    // 5. Ensure data is 32 bytes, pad with leading zeros if shorter
    if (data.length > 32) {
      throw new Error(`ValidationID raw data is ${data.length} bytes, exceeds 32 bytes`);
    }
    
    // 6. Create a bytes32 hex string for the contract call
    const bytes32Data = ethers.hexlify(
      ethers.concat([
        new Uint8Array(32 - data.length), // Left padding with zeros
        data // The actual data
      ])
    );
    
    // 7. Call the contract's packValidationUptimeMessage function
    const packedMessage = await this.messagingContract.packValidationUptimeMessage(
      bytes32Data,
      BigInt(uptimeSeconds)
    );
    
    // 8. Convert the returned bytes to a Uint8Array
    return ethers.getBytes(packedMessage);
  }
  
  async submitAggregateRequest(unsignedMessage: Uint8Array): Promise<Buffer> {
    const req: AggregatorRequest = {
      message: Buffer.from(unsignedMessage).toString('hex')
    };
    
    if (this.signingSubnetId) {
      req["signing-subnet-id"] = this.signingSubnetId;
    }
    
    if (this.quorumPercentage > 0) {
      req["quorum-percentage"] = this.quorumPercentage;
    }
    
    try {
      const response = await axios.post<AggregatorResponse>(
        `${this.baseUrl}/v1/signatureAggregator/fuji/aggregateSignatures`,
        req,
        { headers: { 'Content-Type': 'application/json' } }
      );
      
      let signedHex = response.data["signed-message"];
      
      // Remove 0x prefix if present
      if (signedHex.startsWith("0x")) {
        signedHex = signedHex.substring(2);
      }
      
      return Buffer.from(signedHex, 'hex');
    } catch (error) {
      if (axios.isAxiosError(error) && error.response) {
        let errorMsg = `Aggregator request failed with status code: ${error.response.status}`;
        if (error.response.data && error.response.data.error) {
          errorMsg = `Aggregator error: ${error.response.data.error}`;
        }
        throw new Error(errorMsg);
      }
      throw error;
    }
  }
}