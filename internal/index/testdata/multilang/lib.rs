use std::collections::HashMap;

pub const MAX_CONNECTIONS: usize = 100;

pub trait Storage {
    fn get(&self, key: &str) -> Option<String>;
    fn set(&mut self, key: &str, value: String);
}

pub struct MemoryStore {
    data: HashMap<String, String>,
}

impl MemoryStore {
    pub fn new() -> Self {
        MemoryStore {
            data: HashMap::new(),
        }
    }
}

impl Storage for MemoryStore {
    fn get(&self, key: &str) -> Option<String> {
        self.data.get(key).cloned()
    }

    fn set(&mut self, key: &str, value: String) {
        self.data.insert(key.to_string(), value);
    }
}

fn internal_helper() -> bool {
    true
}
