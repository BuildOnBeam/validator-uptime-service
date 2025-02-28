import axios from 'axios';
import * as crypto from 'crypto';
import * as bs58 from 'bs58';

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

function concatenateUint8Arrays(...arrays: Uint8Array[]): Uint8Array {
  const totalLength = arrays.reduce((acc, arr) => acc + arr.length, 0);
  const result = new Uint8Array(totalLength);
  let offset = 0;
  for (const arr of arrays) {
    result.set(arr, offset);
    offset += arr.length;
  }
  return result;
}

function encodeNumber(num: number | bigint, bytes: number): Uint8Array {
  const buffer = new Uint8Array(bytes);
  
  if (typeof num === 'bigint') {
    // Handle bigint for uint64
    for (let i = bytes - 1; i >= 0; i--) {
      const byte = Number(num & BigInt(0xff));
      buffer[bytes - 1 - i] = byte;
      num = num >> BigInt(8);
    }
  } else {
    // Handle regular number for uint16/uint32
    for (let i = 0; i < bytes; i++) {
      const byte = (num >> (8 * (bytes - 1 - i))) & 0xff;
      buffer[i] = byte;
    }
  }
  
  return buffer;
}

const encodeUint16 = (num: number): Uint8Array => encodeNumber(num, 2);
const encodeUint32 = (num: number): Uint8Array => encodeNumber(num, 4);
const encodeUint64 = (num: bigint): Uint8Array => encodeNumber(num, 8);

function encodeVarBytes(bytes: Uint8Array): Uint8Array {
  const length = encodeUint32(bytes.length);
  return concatenateUint8Arrays(length, bytes);
}

function newAddressedCall(sourceAddress: Uint8Array, payload: Uint8Array): Uint8Array {
  const parts: Uint8Array[] = [];

  parts.push(encodeUint16(CODEC_VERSION));
  parts.push(encodeUint32(1));
  parts.push(encodeVarBytes(sourceAddress));
  parts.push(encodeVarBytes(payload));

  return concatenateUint8Arrays(...parts);
}

function newUnsignedMessage(networkID: number, sourceChainID: string, message: Uint8Array): Uint8Array {
  const parts: Uint8Array[] = [];

  parts.push(encodeUint16(CODEC_VERSION));

  parts.push(encodeUint32(networkID));

  parts.push(bs58.decode(sourceChainID));

  parts.push(encodeUint32(message.length));
  parts.push(message);

  return concatenateUint8Arrays(...parts);
}

export class AggregatorClient {
  baseUrl: string;
  signingSubnetId: string;
  quorumPercentage: number;
  networkID: number;
  sourceChainID: string;
  
  constructor(
    baseUrl: string, 
    signingSubnetId: string = "", 
    quorumPercentage: number = 0,
    networkID: number = 5, // Default to Fuji network ID
    sourceChainID: string = "11111111111111111111111111111111LpoYY" // Default P-Chain ID
  ) {
    this.baseUrl = baseUrl;
    this.signingSubnetId = signingSubnetId;
    this.quorumPercentage = quorumPercentage;
    this.networkID = networkID;
    this.sourceChainID = sourceChainID;
  }
  
  /**
   * Packs a ValidationUptimeMessage into a byte array and wraps it in the proper message format.
   * The message format specification is:
   * +--------------+----------+----------+
   * |      codecID :   uint16 |  2 bytes |
   * +--------------+----------+----------+
   * |       typeID :   uint32 |  4 bytes |
   * +--------------+----------+----------+
   * | validationID : [32]byte | 32 bytes |
   * +--------------+----------+----------+
   * |       uptime :   uint64 |  8 bytes |
   * +--------------+----------+----------+
   *                           | 46 bytes |
   *                           +----------+
   */
  async packValidationUptimeMessage(validationId: string, uptimeSeconds: number): Promise<Uint8Array> {
    let validationIdBytes: Uint8Array;
    
    // 1. Decode the base58 string
    const decoded = bs58.decode(validationId);
    if (decoded.length < 4) {
      throw new Error("Decoded validationID is too short");
    }
    
    // 2. Separate data bytes and the 4-byte checksum
    const data = decoded.slice(0, decoded.length - 4);
    
    // 3. Ensure data is 32 bytes, pad with leading zeros if shorter
    if (data.length > 32) {
      throw new Error(`ValidationID raw data is ${data.length} bytes, exceeds 32 bytes`);
    }
    
    validationIdBytes = new Uint8Array(32);
    // Right-align the data in the 32-byte array (pad front with zeros)
    validationIdBytes.set(data, 32 - data.length);
    
    // 4. Create the message payload with the proper format
    const messagePayload = concatenateUint8Arrays(
      encodeUint16(CODEC_VERSION),
      encodeUint32(VALIDATION_UPTIME_MESSAGE_TYPE_ID),
      validationIdBytes,
      encodeUint64(BigInt(uptimeSeconds))
    );
    
    // 5. Create addressed call with empty source address
    const addressedCall = newAddressedCall(new Uint8Array([]), messagePayload);
    
    // 6. Create unsigned message
    console.log(this.sourceChainID, validationId);
    return newUnsignedMessage(this.networkID, this.sourceChainID, addressedCall);
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
          console.log("Error details:", JSON.stringify(error.response.data));
        }
        throw new Error(errorMsg);
      }
      throw error;
    }
  }
}