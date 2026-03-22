## ADDED Requirements

### Requirement: Runtime container image is slimmed for deployment
The project SHALL provide a slim production-oriented application image that contains only the compiled backend, required frontend assets, and minimal runtime dependencies.

#### Scenario: Building the production image
- **WHEN** the Docker build for deployment completes
- **THEN** the final runtime image MUST exclude unnecessary build toolchains and SQLite runtime dependencies

#### Scenario: Starting the production container
- **WHEN** the runtime container starts
- **THEN** it MUST launch the application with PostgreSQL-oriented configuration and required runtime certificates or OS packages present

### Requirement: Container orchestration provisions PostgreSQL explicitly
The project SHALL provide container runtime wiring that runs the application against a PostgreSQL service instead of a mounted SQLite file.

#### Scenario: Local or hosted compose startup
- **WHEN** operators start the compose stack
- **THEN** a PostgreSQL service MUST be provisioned with persistent storage and the app service MUST receive PostgreSQL connection settings

#### Scenario: PostgreSQL is not yet ready
- **WHEN** the app container starts before PostgreSQL is accepting connections
- **THEN** the runtime configuration or startup flow MUST prevent the app from declaring successful startup until PostgreSQL becomes reachable or startup fails clearly

### Requirement: Operational assets assume PostgreSQL cutover
Deployment scripts, environment examples, and make targets SHALL reflect PostgreSQL as the supported persisted store for the application.

#### Scenario: Operator reviews environment configuration
- **WHEN** an operator uses the provided env example or deployment defaults
- **THEN** the documented database variables MUST reference PostgreSQL configuration instead of SQLite file paths

#### Scenario: Operator uses project build and run automation
- **WHEN** make targets or deployment scripts are used to build or run the stack
- **THEN** they MUST no longer assume creation, cleanup, or mounting of `trading.db`-style SQLite files
