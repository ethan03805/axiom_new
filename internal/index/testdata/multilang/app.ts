import { Request, Response } from 'express';
import { AuthService } from './auth';

export interface Logger {
  info(msg: string): void;
  error(msg: string): void;
}

export class AppServer {
  private auth: AuthService;

  constructor(auth: AuthService) {
    this.auth = auth;
  }

  handleRequest(req: Request, res: Response): void {
    this.auth.verify(req.headers.authorization || '');
    res.send('ok');
  }
}

export function createServer(port: number): AppServer {
  const auth = new AuthService();
  return new AppServer(auth);
}

export const DEFAULT_PORT = 3000;
