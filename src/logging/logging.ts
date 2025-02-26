export const LevelInfo = "info";
export const LevelError = "error";

let currentLevel = LevelInfo;

export function setLevel(level: string): void {
  level = level.toLowerCase();
  if (level !== LevelInfo && level !== LevelError) {
    level = LevelInfo;
  }
  currentLevel = level;
  console.log(`Log level set to ${currentLevel}`);
}

export function info(msg: string): void {
  if (currentLevel === LevelInfo) {
    console.log(`[INFO] ${msg}`);
  }
}

export function infof(format: string, ...args: any[]): void {
  if (currentLevel === LevelInfo) {
    console.log(`[INFO] ${format}`, ...args);
  }
}

export function error(msg: string): void {
  console.log(`[ERROR] ${msg}`);
}

export function errorf(format: string, ...args: any[]): void {
  console.log(`[ERROR] ${format}`, ...args);
}
