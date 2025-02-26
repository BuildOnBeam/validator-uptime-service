import * as logging from '../logging/logging';

export function handleError(context: string, err: Error | null): boolean {
  if (err) {
    logging.errorf("%s: %v", context, err);
    return true;
  }
  return false;
}
