import axios from 'axios';

export interface ValidatorUptime {
  validationId: string;
  nodeId: string;
  uptimeSeconds: number;
}

interface RpcResponse {
  jsonrpc: string;
  result?: {
    validators: Array<{
      validationID: string;
      nodeID: string;
      uptimeSeconds: number;
    }>;
  };
  error?: {
    code: number;
    message: string;
  };
}

export async function fetchUptimes(apiBaseUrl: string): Promise<ValidatorUptime[]> {
  const reqBody = {
    jsonrpc: "2.0",
    id: 1,
    method: "validators.getCurrentValidators",
    params: {}
  };
  
  try {
    const url = `${apiBaseUrl}/validators`;
    const response = await axios.post<RpcResponse>(url, reqBody, {
      headers: { 'Content-Type': 'application/json' }
    });
    
    if (response.status !== 200) {
      throw new Error(`Unexpected status code ${response.status} from Avalanche API`);
    }
    
    const rpcResp = response.data;
    
    if (rpcResp.error) {
      throw new Error(`Avalanche API error ${rpcResp.error.code}: ${rpcResp.error.message}`);
    }
    
    if (!rpcResp.result) {
      throw new Error("Invalid response: missing result");
    }
    
    return rpcResp.result.validators.map(v => ({
      validationId: v.validationID,
      nodeId: v.nodeID,
      uptimeSeconds: v.uptimeSeconds
    }));
  } catch (error) {
    if (error instanceof Error) {
      throw new Error(`Failed to call Avalanche validators API: ${error.message}`);
    }
    throw new Error('Unknown error fetching validator uptimes');
  }
}
