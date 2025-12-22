import type {ReactNode} from 'react';
import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

type FeatureItem = {
  title: string;
  emoji: string;
  description: ReactNode;
};

const FeatureList: FeatureItem[] = [
  {
    title: 'One-Click Environments',
    emoji: '🚀',
    description: (
      <>
        Launch ephemeral Kubernetes environments instantly with a single API call.
        Each user gets isolated namespaces with automatic cleanup when TTL expires.
      </>
    ),
  },
  {
    title: 'GitOps Native',
    emoji: '🔄',
    description: (
      <>
        Built on ArgoCD for true GitOps workflow. Deploy Helm charts from any Git
        repository with automatic sync, self-healing, and version control.
      </>
    ),
  },
  {
    title: 'Secure by Default',
    emoji: '🔐',
    description: (
      <>
        JWT/OIDC authentication with JWKS validation. Per-user quotas, automatic
        TTL-based cleanup, and isolated namespaces for each environment.
      </>
    ),
  },
];

function Feature({title, emoji, description}: FeatureItem) {
  return (
    <div className={clsx('col col--4')}>
      <div className="text--center">
        <span style={{fontSize: '4rem'}}>{emoji}</span>
      </div>
      <div className="text--center padding-horiz--md">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageFeatures(): ReactNode {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
