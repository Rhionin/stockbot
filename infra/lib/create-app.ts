import { App, type AppProps } from 'aws-cdk-lib/core';
import { STAGE_NAMES, resolveStageEnv } from './stage-env.js';
import { StockbotStage } from './stockbot-stage.js';

export function createApp(props?: AppProps): App {
  const app = new App(props);

  for (const stageName of STAGE_NAMES) {
    new StockbotStage(app, stageName, {
      env: resolveStageEnv(app, stageName),
      stageName,
    });
  }

  return app;
}
